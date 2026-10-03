// Package reminders sends credit card payment reminders to an
// Apprise-compatible endpoint (apprise-api, or Lookout's /notify/<key>)
// before each statement's due date, while it's unpaid.
package reminders

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
	"github.com/audemed44/ledger/internal/store"
)

// Overdue is the reminder step sent once the due date has passed.
const Overdue = -1

// overdueWindow is how many days after the due date the overdue reminder
// may still go out, so old statements don't all remind at once.
const overdueWindow = 3

// startHour is the local hour reminders start going out each day.
const startHour = 9

// Notifier sends the reminders.
type Notifier struct {
	Store *store.Store
	// URL is the Apprise endpoint; empty turns reminders off.
	URL string
	// Days are how many days before the due date to remind, e.g. 5, 1, 0.
	Days   []int
	Client *http.Client
}

// ParseDays reads LEDGER_REMINDER_DAYS: comma-separated days before the
// due date, each 0–30.
func ParseDays(value string) ([]int, error) {
	out := []int{}
	for _, part := range strings.Split(value, ",") {
		if part = strings.TrimSpace(part); part == "" {
			continue
		}
		n, err := strconv.Atoi(part)
		if err != nil || n < 0 || n > 30 {
			return nil, fmt.Errorf("%q is not a number of days from 0 to 30", part)
		}
		if !slices.Contains(out, n) {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return out, nil
}

// Enabled reports whether reminders go anywhere.
func (n *Notifier) Enabled() bool { return n != nil && n.URL != "" }

// Run checks every half hour until ctx ends.
func (n *Notifier) Run(ctx context.Context) {
	if !n.Enabled() {
		return
	}
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for {
		if err := n.Check(ctx, time.Now()); err != nil {
			slog.Warn("payment reminders", "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Step is the reminder due for a statement `days` before its due date, and
// whether there is one: the nearest configured day it's within, or Overdue.
func (n *Notifier) Step(days int) (int, bool) {
	if days < 0 {
		return Overdue, days >= -overdueWindow
	}
	for _, d := range n.Days {
		if days <= d {
			return d, true
		}
	}
	return 0, false
}

// Check sends each unpaid card's reminder for now, once. Steps skipped
// while Ledger was down aren't caught up: only the current one is sent.
func (n *Notifier) Check(ctx context.Context, now time.Time) error {
	if !n.Enabled() || now.Hour() < startHour {
		return nil
	}
	dues, err := n.Store.Dues(now)
	if err != nil {
		return err
	}
	var errs []error
	for _, d := range dues {
		if d.Status == "paid" {
			continue
		}
		step, ok := n.Step(d.Days)
		if !ok {
			continue
		}
		sent, err := n.Store.ReminderSent(d.AccountID, d.DueDate, step)
		if err != nil || sent {
			errs = append(errs, err)
			continue
		}
		title, body := Message(d)
		if err = n.Send(ctx, title, body, level(d)); err != nil {
			errs = append(errs, err)
			continue
		}
		errs = append(errs, n.Store.MarkReminderSent(d.AccountID, d.DueDate, step, now))
	}
	return errors.Join(errs...)
}

func level(d store.Due) string {
	if d.Status == "overdue" || d.Days <= 1 {
		return "warning"
	}
	return "info"
}

// When says when a due date is, from the days left.
func When(days int) string {
	switch {
	case days < -1:
		return strconv.Itoa(-days) + " days overdue"
	case days == -1:
		return "1 day overdue"
	case days == 0:
		return "today"
	case days == 1:
		return "tomorrow"
	}
	return "in " + strconv.Itoa(days) + " days"
}

// Message is a reminder's title and body.
func Message(d store.Due) (string, string) {
	title := fmt.Sprintf("%s card ••%s: %s due %s", d.Issuer, d.LastFour, Rupees(d.Remaining), When(d.Days))
	if d.Days < 0 {
		title = fmt.Sprintf("%s card ••%s: %s overdue", d.Issuer, d.LastFour, Rupees(d.Remaining))
	}
	due, _ := time.Parse("2006-01-02", d.DueDate)
	lines := []string{"Due " + due.Format("Mon 2 Jan") + "."}
	if d.MinimumDue > 0 && d.MinimumDue < d.Remaining {
		lines = append(lines, "Minimum "+Rupees(d.MinimumDue)+".")
	}
	if d.Paid > 0 {
		lines = append(lines, Rupees(d.Paid)+" of "+Rupees(d.TotalDue)+" paid since the statement.")
	}
	return title, strings.Join(lines, " ")
}

// Rupees formats minor units as ₹1,23,456.78.
func Rupees(v int64) string { return ledger.Money(v, "INR") }

// Send posts one notification in Apprise's JSON body.
func (n *Notifier) Send(ctx context.Context, title, body, level string) error {
	raw, _ := json.Marshal(map[string]string{"title": title, "body": body, "type": level})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, n.URL, bytes.NewReader(raw))
	if err != nil {
		return errors.New("invalid LEDGER_NOTIFY_URL")
	}
	req.Header.Set("Content-Type", "application/json")
	client := n.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("could not reach %s", req.URL.Host)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent {
		return errors.New("the notification endpoint has nowhere to send it (HTTP 204)")
	}
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("notification endpoint answered HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(msg))
	}
	return nil
}
