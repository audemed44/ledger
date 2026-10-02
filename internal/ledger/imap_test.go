package ledger

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net"
	"testing"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/backend/memory"
	"github.com/emersion/go-imap/client"
	"github.com/emersion/go-imap/server"
)

func TestIMAPLabelCursorBackfillAndPeek(t *testing.T) {
	for _, backfill := range []bool{false, true} {
		name := "new-mail-only"
		if backfill {
			name = "backfill"
		}
		t.Run(name, func(t *testing.T) {
			be := memory.New()
			user, e := be.Login(nil, "username", "password")
			if e != nil {
				t.Fatal(e)
			}
			if e = user.CreateMailbox("Bank"); e != nil {
				t.Fatal(e)
			}
			mailbox, e := user.GetMailbox("Bank")
			if e != nil {
				t.Fatal(e)
			}
			if e = mailbox.CreateMessage(nil, time.Now(), bytes.NewReader(testMail("old", testBody))); e != nil {
				t.Fatal(e)
			}
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			srv := server.New(be)
			srv.AllowInsecureAuth = true
			done := make(chan struct{})
			go func() { defer close(done); srv.Serve(listener) }()
			t.Cleanup(func() { srv.Close(); <-done })
			c, e := client.Dial(listener.Addr().String())
			if e != nil {
				t.Fatal(e)
			}
			c.Timeout = 5 * time.Second
			t.Cleanup(func() { c.Logout() })
			if e = c.Login("username", "password"); e != nil {
				t.Fatal(e)
			}
			s := testStore(t)
			if _, e = s.SaveParser(testParser()); e != nil {
				t.Fatal(e)
			}
			p := Poller{Store: s, Config: MailConfig{User: "username", Label: "Bank", Backfill: backfill}}
			if e = p.syncMailbox(c); e != nil {
				t.Fatal(e)
			}
			rows, e := s.Transactions(Filter{})
			want := 0
			if backfill {
				want = 1
			}
			if e != nil || len(rows) != want {
				t.Fatalf("initial: %d %v", len(rows), e)
			}
			// Append through the protocol, then resume with the existing baseline.
			if e = c.Append("Bank", nil, time.Now(), bytes.NewReader(testMail("new", testBody))); e != nil {
				t.Fatal(e)
			}
			if e = p.syncMailbox(c); e != nil {
				t.Fatal(e)
			}
			rows, e = s.Transactions(Filter{})
			if e != nil || len(rows) != want+1 {
				t.Fatalf("resume: %d %v", len(rows), e)
			}
			if e = p.syncMailbox(c); e != nil {
				t.Fatal(e)
			}
			rows, _ = s.Transactions(Filter{})
			if len(rows) != want+1 {
				t.Fatal("duplicated messages")
			}
			// Inbox has a seed message in the fake server; it must never be imported.
			var count int
			s.DB.QueryRow("SELECT count(*) FROM messages").Scan(&count)
			if count != want+1 {
				t.Fatal("read another mailbox")
			}
			set := new(imap.SeqSet)
			set.AddRange(1, 0)
			messages := make(chan *imap.Message, 10)
			if e = c.Fetch(set, []imap.FetchItem{imap.FetchFlags}, messages); e != nil {
				t.Fatal(e)
			}
			for msg := range messages {
				for _, f := range msg.Flags {
					if f == imap.SeenFlag {
						t.Fatal("changed read status")
					}
				}
			}
			// Simulate server UIDVALIDITY reset: replay must still deduplicate by Message-ID.
			identity := sha256.Sum256([]byte("username\x00Bank"))
			key := "cursor:" + hex.EncodeToString(identity[:])
			v, e := s.Setting(key)
			if e != nil {
				t.Fatal(e)
			}
			var cur cursor
			json.Unmarshal([]byte(v), &cur)
			cur.Validity = 99
			raw, _ := json.Marshal(cur)
			s.SetSetting(key, string(raw))
			if e = p.syncMailbox(c); e != nil {
				t.Fatal(e)
			}
			rows, _ = s.Transactions(Filter{})
			if len(rows) != 2 {
				t.Fatalf("UID reset should recover both messages once, got %d", len(rows))
			}
		})
	}
}
