package store

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/audemed44/ledger/internal/ledger"
)

// Insights describe a month's spending: debits that aren't transfers or
// dismissed, in the currency most is spent in, less refunds. A refund is a
// credit with a category (a merchant's refund, a reversed fee, money paid
// back): it lowers its category in the month it arrives, never below zero.
// Credits without one (salary, say) are income and leave spending alone.
// Averages are over the six months before, from the first month Ledger has
// anything for.

// TrendMonths is how many months the trend covers, ending with the month.
const TrendMonths = 12

// BaselineMonths is how many months before the month its averages cover.
const BaselineMonths = 6

// MonthTotal is one month's spending.
type MonthTotal struct {
	Month  string `json:"month"`
	Amount int64  `json:"amount"`
}

// CategoryInsight is one category's spending in the month, the month
// before, and on average.
type CategoryInsight struct {
	CategoryID int64  `json:"category_id"`
	Category   string `json:"category"`
	Amount     int64  `json:"amount"`
	Count      int    `json:"count"`
	Previous   int64  `json:"previous"`
	Average    int64  `json:"average"`
	// Refunds is what came back in the category this month, already taken
	// off Amount.
	Refunds int64 `json:"refunds"`
	// Months is the category's spending per trend month, oldest first.
	Months []int64 `json:"months"`
}

// Insight is one observation, in words. Tone is "up" (more spent than
// usual), "down" (less) or "info".
type Insight struct {
	Text string `json:"text"`
	Tone string `json:"tone"`
}

// Insights is the Insights page for one month.
type Insights struct {
	Month    string `json:"month"`
	Currency string `json:"currency"`
	Total    int64  `json:"total"`
	Count    int    `json:"count"`
	// Refunds is what came back this month, already taken off Total.
	Refunds  int64 `json:"refunds"`
	Previous int64 `json:"previous"`
	// Average is the mean of up to BaselineMonths months before, over
	// BaselineCount months (0 when there are none).
	Average       int64 `json:"average"`
	BaselineCount int   `json:"baseline_count"`
	// Days is how many of the month's days have passed (all of them for a
	// past month). Projected, for the current month only, is what's spent
	// so far plus the everyday rate (spends under EverydayLimit) for the
	// days left: rent and other big one-offs aren't repeated.
	Days        int   `json:"days"`
	DaysInMonth int   `json:"days_in_month"`
	Projected   int64 `json:"projected,omitempty"`
	Current     bool  `json:"current"`
	// Months is the trend, oldest first, ending with the month.
	Months     []MonthTotal      `json:"months"`
	Categories []CategoryInsight `json:"categories"`
	// Daily and PreviousDaily are spending per day of the month and of the
	// month before.
	Daily         []int64              `json:"daily"`
	PreviousDaily []int64              `json:"previous_daily"`
	Largest       []ledger.Transaction `json:"largest"`
	Insights      []Insight            `json:"insights"`
}

// EverydayLimit is the largest spend counted in the everyday rate.
const EverydayLimit = 500000 // ₹5,000

// spendingWhere picks the debits that count as spending.
const spendingWhere = "t.direction='debit' AND t.transfer='' AND t.status!='dismissed'"

// refundWhere picks the credits that count against spending.
const refundWhere = "t.direction='credit' AND t.transfer='' AND t.status!='dismissed' AND t.category_id IS NOT NULL"

// MonthInsights works out the Insights for month (YYYY-MM), as of now.
func (s *Store) MonthInsights(month string, now time.Time) (Insights, error) {
	start, err := time.ParseInLocation("2006-01", month, now.Location())
	if err != nil {
		return Insights{}, err
	}
	out := Insights{Month: month, DaysInMonth: start.AddDate(0, 1, -1).Day()}
	out.Current = start.Year() == now.Year() && start.Month() == now.Month()
	out.Days = out.DaysInMonth
	if out.Current {
		out.Days = now.Day()
	} else if start.After(now) {
		out.Days = 0
	}

	// The currency most was spent in over the trend.
	first := start.AddDate(0, -(TrendMonths - 1), 0).Format("2006-01")
	err = s.DB.QueryRow(`SELECT currency FROM transactions t WHERE `+spendingWhere+`
  AND substr(t.date,1,7) BETWEEN ? AND ? GROUP BY currency ORDER BY sum(amount) DESC LIMIT 1`,
		first, month).Scan(&out.Currency)
	if err != nil {
		out.Currency = "INR"
	}

	// Per month and category, over the trend and the baseline before it.
	from := start.AddDate(0, -(TrendMonths - 1 + BaselineMonths), 0).Format("2006-01")
	rows, err := s.DB.Query(`SELECT substr(t.date,1,7),coalesce(c.id,0),coalesce(c.name,''),
  sum(CASE WHEN t.direction='debit' THEN t.amount ELSE 0 END),
  sum(CASE WHEN t.direction='credit' THEN t.amount ELSE 0 END),
  sum(t.direction='debit')
FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
WHERE ((`+spendingWhere+`) OR (`+refundWhere+`)) AND t.currency=? AND substr(t.date,1,7) BETWEEN ? AND ?
GROUP BY 1,2`, out.Currency, from, month)
	if err != nil {
		return out, err
	}
	type key struct {
		month    string
		category int64
	}
	amounts, refunds, counts, names := map[key]int64{}, map[key]int64{}, map[key]int{}, map[int64]string{}
	monthly := map[string]int64{}
	for rows.Next() {
		var m, name string
		var category, spent, back int64
		var count int
		if err = rows.Scan(&m, &category, &name, &spent, &back, &count); err != nil {
			rows.Close()
			return out, err
		}
		k := key{m, category}
		amounts[k], refunds[k], counts[k], names[category] = max(0, spent-back), back, count, name
		monthly[m] += amounts[k]
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return out, err
	}
	var earliest string
	if err = s.DB.QueryRow("SELECT coalesce(min(substr(date,1,7)),'') FROM transactions").Scan(&earliest); err != nil {
		return out, err
	}

	monthAt := func(i int) string { return start.AddDate(0, i, 0).Format("2006-01") }
	previous := monthAt(-1)
	baseline := []string{}
	for i := 1; i <= BaselineMonths; i++ {
		if m := monthAt(-i); earliest != "" && m >= earliest {
			baseline = append(baseline, m)
		}
	}
	out.BaselineCount = len(baseline)
	average := func(of func(string) int64) int64 {
		if len(baseline) == 0 {
			return 0
		}
		var sum int64
		for _, m := range baseline {
			sum += of(m)
		}
		return int64(math.Round(float64(sum) / float64(len(baseline))))
	}
	out.Total, out.Previous = monthly[month], monthly[previous]
	out.Average = average(func(m string) int64 { return monthly[m] })
	for i := TrendMonths - 1; i >= 0; i-- {
		m := monthAt(-i)
		out.Months = append(out.Months, MonthTotal{m, monthly[m]})
	}
	if out.Current && out.Days >= 1 {
		var everyday int64
		if err = s.DB.QueryRow(`SELECT coalesce(sum(t.amount),0) FROM transactions t WHERE `+spendingWhere+`
  AND t.currency=? AND substr(t.date,1,7)=? AND t.amount<?`, out.Currency, month, EverydayLimit).Scan(&everyday); err != nil {
			return out, err
		}
		out.Projected = out.Total + int64(math.Round(float64(everyday)/float64(out.Days)*float64(out.DaysInMonth-out.Days)))
	}

	// Every category spent in during the trend or baseline.
	for category, name := range names {
		c := CategoryInsight{CategoryID: category, Category: name,
			Amount: amounts[key{month, category}], Count: counts[key{month, category}],
			Previous: amounts[key{previous, category}], Refunds: refunds[key{month, category}],
			Average: average(func(m string) int64 { return amounts[key{m, category}] })}
		for _, m := range out.Months {
			c.Months = append(c.Months, amounts[key{m.Month, category}])
		}
		out.Count += c.Count
		out.Refunds += c.Refunds
		if c.Amount == 0 && c.Count == 0 && c.Average == 0 && c.Refunds == 0 && !anyMonth(c.Months) {
			continue
		}
		out.Categories = append(out.Categories, c)
	}
	sort.Slice(out.Categories, func(i, j int) bool {
		a, b := out.Categories[i], out.Categories[j]
		if a.Amount != b.Amount {
			return a.Amount > b.Amount
		}
		if a.Average != b.Average {
			return a.Average > b.Average
		}
		return a.Category < b.Category
	})

	if out.Daily, err = s.dailySpending(month, out.Currency, out.DaysInMonth); err != nil {
		return out, err
	}
	if out.PreviousDaily, err = s.dailySpending(previous, out.Currency, start.AddDate(0, 0, -1).Day()); err != nil {
		return out, err
	}
	if out.Largest, err = s.largestSpending(month, out.Currency, 5); err != nil {
		return out, err
	}
	out.Insights = observe(out)
	return out, nil
}

// dailySpending is month's spending per day, less that day's refunds.
func (s *Store) dailySpending(month, currency string, days int) ([]int64, error) {
	out := make([]int64, days)
	rows, err := s.DB.Query(`SELECT CAST(substr(t.date,9,2) AS INTEGER),
  sum(CASE WHEN t.direction='debit' THEN t.amount ELSE -t.amount END) FROM transactions t
WHERE ((`+spendingWhere+`) OR (`+refundWhere+`)) AND t.currency=? AND substr(t.date,1,7)=? GROUP BY 1`, currency, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var day int
		var amount int64
		if err = rows.Scan(&day, &amount); err != nil {
			return nil, err
		}
		if day >= 1 && day <= days {
			out[day-1] = amount
		}
	}
	return out, rows.Err()
}

func anyMonth(months []int64) bool {
	for _, v := range months {
		if v != 0 {
			return true
		}
	}
	return false
}

// largestSpending is month's n largest debits.
func (s *Store) largestSpending(month, currency string, n int) ([]ledger.Transaction, error) {
	rows, err := s.DB.Query(`SELECT t.id,t.merchant,t.amount,t.currency,t.date,t.issuer,t.account,t.account_kind,
  coalesce(c.id,0),coalesce(c.name,'')
FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
WHERE `+spendingWhere+` AND t.currency=? AND substr(t.date,1,7)=? ORDER BY t.amount DESC,t.id LIMIT ?`, currency, month, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []ledger.Transaction{}
	for rows.Next() {
		t := ledger.Transaction{Direction: "debit"}
		if err = rows.Scan(&t.ID, &t.Merchant, &t.Amount, &t.Currency, &t.Date, &t.Issuer, &t.Account,
			&t.AccountKind, &t.CategoryID, &t.Category); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// payeeName is a UPI description's payee: "VPA …@okhdfc (A SHOP)".
var payeeName = regexp.MustCompile(`\(([^()]{3,})\)\s*$`)

// Payee is who a description names: the payee in brackets when there is
// one, else the whole description.
func Payee(merchant string) string {
	if m := payeeName.FindStringSubmatch(merchant); m != nil {
		return strings.TrimSpace(m[1])
	}
	return strings.TrimSpace(merchant)
}

// rupees is an amount in whole units, for sentences: ₹34,018.
func rupees(v int64, currency string) string {
	return strings.TrimSuffix(ledger.Money(int64(math.Round(float64(v)/100))*100, currency), ".00")
}

// percent is how far a is from b, as a whole percentage.
func percent(a, b int64) int {
	return int(math.Round((float64(a) - float64(b)) / float64(b) * 100))
}

// observe picks out what's worth saying about a month: its pace against
// the usual, the categories furthest from their usual, new ones, the
// largest spend, and how much is uncategorised. Small amounts and
// differences are left out.
func observe(in Insights) []Insight {
	out := []Insight{}
	money := func(v int64) string { return rupees(v, in.Currency) }
	const floor = 200000 // ₹2,000: smaller differences aren't worth a line
	usual := fmt.Sprintf("your %d-month average", in.BaselineCount)
	if in.BaselineCount == 1 {
		usual = "last month"
	}

	// The month against the usual: its projection while it's running.
	total, verb := in.Total, "Spent"
	if in.Current {
		total, verb = in.Projected, "On pace for"
	}
	if in.Average > 0 && total > 0 && (!in.Current || in.Days >= 5) {
		p := percent(total, in.Average)
		tone, how := "info", "in line with "+usual
		switch {
		case p >= 10:
			tone, how = "up", fmt.Sprintf("%d%% above %s (%s)", p, usual, money(in.Average))
		case p <= -10:
			tone, how = "down", fmt.Sprintf("%d%% below %s (%s)", -p, usual, money(in.Average))
		}
		out = append(out, Insight{fmt.Sprintf("%s %s this month, %s.", verb, money(total), how), tone})
	}

	// Categories furthest from their usual, up to two up and one down.
	if in.BaselineCount > 0 {
		type move struct {
			c    CategoryInsight
			diff int64
		}
		ups, downs := []move{}, []move{}
		for _, c := range in.Categories {
			if c.CategoryID == 0 {
				continue
			}
			diff := c.Amount - c.Average
			switch {
			case c.Average == 0 && c.Amount >= floor:
				out = append(out, Insight{fmt.Sprintf("%s is new: %s this month, nothing in the %d months before.",
					c.Category, money(c.Amount), in.BaselineCount), "up"})
			case c.Average > 0 && diff >= floor && c.Amount >= c.Average*5/4:
				ups = append(ups, move{c, diff})
			case c.Average > 0 && -diff >= floor && c.Amount <= c.Average*3/4 && !in.Current:
				// A running month hasn't finished spending, so "down" waits for it to end.
				downs = append(downs, move{c, diff})
			}
		}
		sort.Slice(ups, func(i, j int) bool { return ups[i].diff > ups[j].diff })
		sort.Slice(downs, func(i, j int) bool { return downs[i].diff < downs[j].diff })
		for i, m := range ups {
			if i == 2 {
				break
			}
			times := float64(m.c.Amount) / float64(m.c.Average)
			how := fmt.Sprintf("%d%% above its usual", percent(m.c.Amount, m.c.Average))
			if times >= 2 {
				how = strings.Replace(fmt.Sprintf("%.1f× its usual", times), ".0×", "×", 1)
			}
			out = append(out, Insight{fmt.Sprintf("%s: %s, %s %s.", m.c.Category, money(m.c.Amount), how,
				money(m.c.Average)), "up"})
		}
		if len(downs) > 0 {
			m := downs[0]
			out = append(out, Insight{fmt.Sprintf("%s: %s, %d%% below its usual %s.", m.c.Category, money(m.c.Amount),
				-percent(m.c.Amount, m.c.Average), money(m.c.Average)), "down"})
		}
	}

	if len(in.Largest) > 0 && in.Total > 0 {
		t := in.Largest[0]
		date, _ := time.Parse("2006-01-02", t.Date[:min(10, len(t.Date))])
		share := fmt.Sprintf("%d%% of the month", int(math.Round(float64(t.Amount)/float64(in.Total)*100)))
		if t.Category != "" {
			share = t.Category + ", " + share
		}
		out = append(out, Insight{fmt.Sprintf("Largest single spend: %s on %s, %s (%s).",
			money(t.Amount), date.Format("2 Jan"), Payee(t.Merchant), share), "info"})
	}
	for _, c := range in.Categories {
		if c.CategoryID == 0 && c.Amount > 0 && in.Total > 0 && c.Amount*20 >= in.Total {
			out = append(out, Insight{fmt.Sprintf("%s (%d%%) is uncategorised, so these numbers are rougher than they could be.",
				money(c.Amount), int(math.Round(float64(c.Amount)/float64(in.Total)*100))), "info"})
		}
	}
	return out
}
