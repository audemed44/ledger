package store

import (
	"regexp"
	"strings"
	"time"
)

// Card bill payments leave a bank account weeks before their other side
// reaches Ledger: the card's credit comes with its next statement. Until
// then a debit described as one of these is a "pending" transfer: out of
// the totals, and still paired when the credit arrives. If none has
// arrived after CardPaymentPendingDays it counts as spending again, shown
// as a card payment with no card credit found (a statement that never
// came, or a card that isn't yours).
//
// Each description must name one of your cards or its issuer, or be a
// channel that only pays card bills; a masked card number alone isn't
// enough, since debit card withdrawals and purchases carry one too.
var (
	// HDFC NetBanking bill pay, with the card's masked number:
	// "IB BILLPAY DR-HDFCWI-552260XXXXXX7149".
	billPayCard = regexp.MustCompile(`(?i)\bBILLPAY DR-[A-Z0-9]+-\d{6}X{6}(\d{4})\b`)
	// BillDesk card bill payments name the issuer: "…/BILLDKHSBCCREDITCARD".
	billDeskCard = regexp.MustCompile(`(?i)\bBILLDK([A-Z]+?)CREDITCARD\b`)
	// Another bank's NetBanking: "INB ICICI Bank Credit card ICICI_CC Payments".
	netBankingCard = regexp.MustCompile(`(?i)^INB\s+([A-Z]+)\s+Bank\s+Credit\s*card\b`)
	// Google Pay's card bill payments, as HDFC (in full or masked) and SBI
	// describe them. The address only pays card bills; it doesn't say
	// which card.
	googlePayCard = []*regexp.Regexp{
		regexp.MustCompile(`(?i)^VPA (?:gpay-credit|\*+)card@okpayaxis \(?Google India Digital Services`),
		regexp.MustCompile(`(?i)^UPI-GOOGLE INDIA DIGITAL-GPAY-CREDITCARD@OKPAYAXIS-`),
		regexp.MustCompile(`(?i)^UPI/DR/\d+/Google I/UTIB/gpay-credi/`),
	}
)

// CardPaymentPendingDays is how long a card bill payment waits for the
// card's credit before it counts as spending again.
const CardPaymentPendingDays = 45

// yourCards are the card accounts Ledger has seen: issuers and last four
// digits, lowercased.
type yourCards struct {
	issuers, numbers map[string]bool
}

func (s *Store) yourCards() (yourCards, error) {
	c := yourCards{issuers: map[string]bool{}, numbers: map[string]bool{}}
	rows, err := s.DB.Query("SELECT DISTINCT lower(trim(issuer)),account FROM transactions WHERE account_kind='card'")
	if err != nil {
		return c, err
	}
	defer rows.Close()
	for rows.Next() {
		var issuer, account string
		if err = rows.Scan(&issuer, &account); err != nil {
			return c, err
		}
		c.issuers[issuer], c.numbers[account] = true, true
	}
	return c, rows.Err()
}

// cardPayment reports whether a bank debit's description is a bill
// payment to one of your cards.
func (c yourCards) cardPayment(merchant string) bool {
	merchant = strings.TrimSpace(merchant)
	if len(c.issuers) == 0 {
		return false
	}
	if m := billPayCard.FindStringSubmatch(merchant); m != nil {
		return c.numbers[m[1]]
	}
	if m := billDeskCard.FindStringSubmatch(merchant); m != nil {
		return c.issuers[strings.ToLower(m[1])]
	}
	if m := netBankingCard.FindStringSubmatch(merchant); m != nil {
		return c.issuers[strings.ToLower(m[1])]
	}
	for _, p := range googlePayCard {
		if p.MatchString(merchant) {
			return true
		}
	}
	return false
}

// now is the store's clock.
func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// findCardPayments marks recent bank debits that are card bill payments,
// and not paired or otherwise marked, as pending transfers. findTransfers
// clears the old marks first. The caller holds s.mu.
func (s *Store) findCardPayments() error {
	cards, err := s.yourCards()
	if err != nil {
		return err
	}
	since := s.now().AddDate(0, 0, -CardPaymentPendingDays).Format("2006-01-02")
	rows, err := s.DB.Query(`SELECT id,merchant FROM transactions WHERE direction='debit' AND account_kind!='card'
  AND transfer='' AND no_transfer=0 AND status!='dismissed' AND substr(date,1,10)>=?`, since)
	if err != nil {
		return err
	}
	pending := []int64{}
	for rows.Next() {
		var id int64
		var merchant string
		if err = rows.Scan(&id, &merchant); err != nil {
			rows.Close()
			return err
		}
		if cards.cardPayment(merchant) {
			pending = append(pending, id)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, id := range pending {
		if _, err = s.DB.Exec("UPDATE transactions SET transfer='pending' WHERE id=?", id); err != nil {
			return err
		}
	}
	return nil
}
