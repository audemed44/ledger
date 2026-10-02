package ledger

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"

	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const maxMail = 25 << 20
const backfillStart = "2026-01-01"
const beforeBackfillReason = "Before 1 January 2026 backfill start"

// MIME is archived intact, including every attachment, before the DB is updated.
// File names come from a hash, never a sender-controlled path or Message-ID.
func (s *Store) Ingest(raw []byte) (int64, error) {
	if len(raw) > maxMail {
		return 0, errors.New("message exceeds 25 MiB")
	}
	hash := sha256.Sum256(raw)
	key := hex.EncodeToString(hash[:])
	m := Message{Key: "sha256:" + key, Archive: key + ".eml", Date: time.Now().UTC().Format(time.RFC3339)}
	m = decodeMail(raw, m)
	// The same mail may be fetched after a crash or a UIDVALIDITY change.
	var id int64
	err := s.DB.QueryRow("SELECT id FROM messages WHERE message_key=?", m.Key).Scan(&id)
	if err == nil {
		return id, s.Process(id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	path := filepath.Join(s.Dir, "archive", m.Archive)
	temp, err := os.CreateTemp(filepath.Dir(path), ".mail-*")
	if err != nil {
		return 0, err
	}
	defer os.Remove(temp.Name())
	if _, err = temp.Write(raw); err != nil {
		temp.Close()
		return 0, err
	}
	if err = temp.Sync(); err != nil {
		temp.Close()
		return 0, err
	}
	if err = temp.Close(); err != nil {
		return 0, err
	}
	if err = os.Rename(temp.Name(), path); err != nil {
		return 0, err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return 0, err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return 0, err
	}
	_, err = s.DB.Exec("INSERT OR IGNORE INTO messages(message_key,sender,subject,date,body,reason,archive,has_pdf) VALUES(?,?,?,?,?,?,?,?)", m.Key, m.Sender, m.Subject, m.Date, m.Body, m.Reason, m.Archive, m.HasPDF)
	if err != nil {
		return 0, err
	}
	if err = s.DB.QueryRow("SELECT id FROM messages WHERE message_key=?", m.Key).Scan(&id); err != nil {
		return 0, err
	}
	return id, s.Process(id)
}

type MailConfig struct {
	User, Password, Label string
	Interval              time.Duration
	Backfill              bool
}
type SyncStatus struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	LastSync   string `json:"last_sync"`
	Error      string `json:"error"`
	Label      string `json:"label"`
	Interval   string `json:"interval"`
}
type Poller struct {
	Store  *Store
	Config MailConfig
	mu     sync.Mutex
	status SyncStatus
}

func (p *Poller) Status() SyncStatus {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Configured = p.Config.User != "" && p.Config.Password != ""
	s.Label = p.Config.Label
	s.Interval = p.Config.Interval.String()
	return s
}
func (p *Poller) Run(ctx context.Context) {
	p.Sync(ctx)
	tick := time.NewTicker(p.Config.Interval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.Sync(ctx)
		}
	}
}
func (p *Poller) Sync(ctx context.Context) {
	p.mu.Lock()
	if p.status.Running || p.Config.User == "" || p.Config.Password == "" {
		p.mu.Unlock()
		return
	}
	p.status.Running = true
	p.mu.Unlock()
	err := p.sync(ctx)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.status.Running = false
	if err != nil {
		p.status.Error = err.Error()
	} else {
		p.status.Error = ""
		p.status.LastSync = time.Now().UTC().Format(time.RFC3339)
	}
}

type cursor struct {
	Validity uint32 `json:"validity"`
	UID      uint32 `json:"uid"`
}

func (p *Poller) sync(ctx context.Context) error {
	// Fixed Gmail TLS endpoint avoids accidental cleartext credential delivery.
	dial := tls.Dialer{NetDialer: &net.Dialer{Timeout: 20 * time.Second}, Config: &tls.Config{ServerName: "imap.gmail.com", MinVersion: tls.VersionTLS12}}
	conn, err := dial.DialContext(ctx, "tcp", "imap.gmail.com:993")
	if err != nil {
		return errors.New("Could not connect to Gmail; check network access")
	}
	defer conn.Close()
	stop := context.AfterFunc(ctx, func() { conn.Close() })
	defer stop()
	conn.SetDeadline(time.Now().Add(10 * time.Minute))
	c, err := client.New(conn)
	if err != nil {
		return errors.New("Gmail IMAP handshake failed")
	}
	c.Timeout = 30 * time.Second
	if err = c.Login(p.Config.User, p.Config.Password); err != nil {
		return errors.New("Gmail login failed; check address and app password")
	}
	defer c.Logout()
	return p.syncMailbox(c)
}

func (p *Poller) syncMailbox(c *client.Client) error {
	box, err := c.Select(p.Config.Label, true)
	if err != nil {
		return errors.New("Could not open configured Gmail label; enable its IMAP visibility")
	}
	identity := sha256.Sum256([]byte(p.Config.User + "\x00" + p.Config.Label))
	setting := "cursor:" + hex.EncodeToString(identity[:])
	saved, err := p.Store.Setting(setting)
	if err != nil {
		return errors.New("Could not read sync cursor")
	}
	var cur cursor
	if saved != "" {
		if err = json.Unmarshal([]byte(saved), &cur); err != nil {
			return errors.New("Invalid stored sync cursor")
		}
	}
	persist := func() error { raw, _ := json.Marshal(cur); return p.Store.SetSetting(setting, string(raw)) }
	if saved == "" && !p.Config.Backfill {
		if box.UidNext == 0 {
			return errors.New("Gmail did not return a UID baseline")
		}
		cur = cursor{Validity: box.UidValidity, UID: box.UidNext - 1}
		return persist()
	}
	if cur.Validity != box.UidValidity {
		cur = cursor{Validity: box.UidValidity}
	}
	if cur.UID == ^uint32(0) {
		return nil
	}
	criteria := imap.NewSearchCriteria()
	// SENTSINCE uses the email's calendar date, including the boundary day.
	// Apply it on resumed backfills and UIDVALIDITY resets as well.
	criteria.SentSince, _ = time.Parse("2006-01-02", backfillStart)
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(cur.UID+1, 0)
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return errors.New("Could not search Gmail label")
	}
	// Bound each pass; resume from the persisted cursor at the next interval.
	processed := 0
	for _, uid := range uids {
		if uid <= cur.UID {
			continue
		}
		if processed >= 500 {
			break
		}
		processed++
		set := new(imap.SeqSet)
		set.AddNum(uid)
		// Ask size first so an oversized message cannot exhaust memory.
		sizes := make(chan *imap.Message, 1)
		if err = c.UidFetch(set, []imap.FetchItem{imap.FetchRFC822Size}, sizes); err != nil {
			return errors.New("Could not read message size")
		}
		metadata := <-sizes
		if metadata == nil {
			continue
		}
		if metadata.Size > maxMail {
			return fmt.Errorf("Message UID %d exceeds 25 MiB; sync paused without skipping it", uid)
		}
		section := &imap.BodySectionName{Peek: true}
		messages := make(chan *imap.Message, 1)
		done := make(chan error, 1)
		go func() { done <- c.UidFetch(set, []imap.FetchItem{section.FetchItem()}, messages) }()
		var ingestErr error
		found := false
		for msg := range messages {
			body := msg.GetBody(section)
			if body == nil {
				continue
			}
			found = true
			raw, e := io.ReadAll(io.LimitReader(body, maxMail+1))
			if e != nil {
				ingestErr = e
				continue
			}
			_, ingestErr = p.Store.Ingest(raw)
		}
		if e := <-done; e != nil {
			return errors.New("Could not fetch message; will retry")
		}
		if ingestErr != nil {
			return errors.New("Could not archive or process message; will retry")
		}
		if !found {
			return errors.New("Gmail returned no message body; will retry")
		}
		cur.UID = uid
		if err = persist(); err != nil {
			return errors.New("Could not save sync cursor")
		}
	}
	return persist()
}

// Synthetic demo data never touches Gmail and lives in the chosen data directory.
func (s *Store) SeedDemo() error {
	const pattern = `(?s)Amount: (?P<currency>[A-Z]{3}) (?P<amount>[\d,.]+)\nMerchant: (?P<merchant>[^\n]+)\nCard: (?P<account>\d{4})\nDate: (?P<date>[^\n]+)\nReference: (?P<reference>[^\n]+)`
	parsers, err := s.Parsers()
	if err != nil {
		return err
	}
	if len(parsers) == 0 {
		_, err = s.SaveParser(Parser{Name: "Example Bank", Sender: "alerts@example.invalid", Subject: "^Purchase alert$", Pattern: pattern, DateLayout: "2006-01-02", Timezone: "Asia/Kolkata", Currency: "INR", Direction: "debit", Enabled: true})
		if err != nil {
			return err
		}
	}
	for i, name := range []string{"Paper & Press", "Neighbourhood Coffee", "Metro Transit", "Sunday Groceries", "Studio Music", "The Reading Room"} {
		date := time.Now().AddDate(0, 0, -i).Format("2006-01-02")
		raw := fmt.Sprintf("From: alerts@example.invalid\r\nSubject: Purchase alert\r\nMessage-ID: <demo-%d@ledger.invalid>\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nAmount: INR %s\nMerchant: %s\nCard: 4242\nDate: %s\nReference: DEMO-%d\n", i, []string{"1290.00", "280.00", "120.00", "2840.50", "149.00", "760.00"}[i], name, date, i)
		if _, err = s.Ingest([]byte(raw)); err != nil {
			return err
		}
	}
	_, err = s.Ingest([]byte("From: notices@example.invalid\r\nSubject: A new alert format\r\nMessage-ID: <demo-unmatched@ledger.invalid>\r\nContent-Type: text/plain\r\n\r\nYour card 8080 was debited INR 450.00 at EXAMPLE SHOP.\n"))
	return err
}
