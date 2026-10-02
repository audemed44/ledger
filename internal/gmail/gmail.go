// Package gmail polls one Gmail label over IMAP and hands every new message
// to the store. It only reads: messages are fetched with PEEK, so nothing is
// moved, deleted or marked read.
package gmail

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"

	"github.com/audemed44/ledger/internal/mail"
	"github.com/audemed44/ledger/internal/store"
)

// batch bounds one pass; the rest is picked up at the next interval.
const batch = 500

// Config is the mailbox to poll. Polling is off without a user and password.
type Config struct {
	User, Password, Label string
	Interval              time.Duration
	// Backfill imports mail already under the label (from store.BackfillStart)
	// the first time the label is synced. Otherwise only later mail is read.
	Backfill bool
}

// Status is what the Connection page shows. Errors never contain
// credentials or message contents.
type Status struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	LastSync   string `json:"last_sync"`
	Error      string `json:"error"`
	Label      string `json:"label"`
	Interval   string `json:"interval"`
}

// Poller syncs the label on an interval, one sync at a time.
type Poller struct {
	Store  *store.Store
	Config Config
	mu     sync.Mutex
	status Status
}

// Status returns the current sync status.
func (p *Poller) Status() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	s.Configured = p.Config.User != "" && p.Config.Password != ""
	s.Label = p.Config.Label
	s.Interval = p.Config.Interval.String()
	return s
}

// Run syncs now and then every interval until ctx ends.
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

// Sync runs one pass, unless polling is off or a pass is already running.
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

func (p *Poller) sync(ctx context.Context) error {
	// A fixed TLS endpoint, so credentials can't go anywhere in cleartext.
	dial := tls.Dialer{
		NetDialer: &net.Dialer{Timeout: 20 * time.Second},
		Config:    &tls.Config{ServerName: "imap.gmail.com", MinVersion: tls.VersionTLS12},
	}
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

// cursor is the last UID handled, per mailbox and UIDVALIDITY.
type cursor struct {
	Validity uint32 `json:"validity"`
	UID      uint32 `json:"uid"`
}

// syncMailbox fetches messages after the saved cursor and moves it forward
// after each one is archived, so an interrupted pass resumes safely. A
// UIDVALIDITY change rescans the label; the store drops duplicates.
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
	persist := func() error {
		raw, _ := json.Marshal(cur)
		return p.Store.SetSetting(setting, string(raw))
	}
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
	// It applies on resumed backfills and UIDVALIDITY resets as well.
	criteria.SentSince, _ = time.Parse("2006-01-02", store.BackfillStart)
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(cur.UID+1, 0)
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return errors.New("Could not search Gmail label")
	}
	processed := 0
	for _, uid := range uids {
		if uid <= cur.UID {
			continue
		}
		if processed >= batch {
			break
		}
		processed++
		fetched, err := p.fetch(c, uid)
		if err != nil {
			return err
		}
		if !fetched {
			continue // gone since the search; the cursor stays put
		}
		cur.UID = uid
		if err = persist(); err != nil {
			return errors.New("Could not save sync cursor")
		}
	}
	return persist()
}

// fetch reads one message and ingests it. It asks for the size first, so an
// oversized message can't exhaust memory; that pauses the sync rather than
// skipping the message. It reports false when the message no longer exists.
func (p *Poller) fetch(c *client.Client, uid uint32) (bool, error) {
	set := new(imap.SeqSet)
	set.AddNum(uid)
	sizes := make(chan *imap.Message, 1)
	if err := c.UidFetch(set, []imap.FetchItem{imap.FetchRFC822Size}, sizes); err != nil {
		return false, errors.New("Could not read message size")
	}
	metadata := <-sizes
	if metadata == nil {
		return false, nil
	}
	if metadata.Size > mail.MaxSize {
		return false, fmt.Errorf("Message UID %d exceeds 25 MiB; sync paused without skipping it", uid)
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
		raw, e := io.ReadAll(io.LimitReader(body, mail.MaxSize+1))
		if e != nil {
			ingestErr = e
			continue
		}
		_, ingestErr = p.Store.Ingest(raw)
	}
	if e := <-done; e != nil {
		return false, errors.New("Could not fetch message; will retry")
	}
	if ingestErr != nil {
		return false, errors.New("Could not archive or process message; will retry")
	}
	if !found {
		return false, errors.New("Gmail returned no message body; will retry")
	}
	return true, nil
}
