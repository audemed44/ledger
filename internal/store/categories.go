package store

import (
	"database/sql"
	"errors"
	"strings"
)

// Categories say what money went on. A transaction's category is set by
// hand (category_source "manual"), or by the most specific category rule
// whose text its description contains ("rule"). Rules are worked out again
// whenever transactions change, so they reach past and future lines alike;
// a hand-set category always wins.

// Category is one of your categories.
type Category struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// DefaultCategories start a new ledger off; they can be renamed or removed.
var DefaultCategories = []string{
	"Rent", "Bills & utilities", "Groceries", "Food delivery", "Eating out",
	"Shopping", "Games", "Subscriptions", "Entertainment", "Travel",
	"Transport", "Health", "Family", "Gifts", "Cash", "Fees & charges", "Other",
}

// CategoryRule puts transactions whose description contains Contains (any
// case), on accounts of Issuer or any account when it's empty, in a
// category.
type CategoryRule struct {
	ID         int64  `json:"id"`
	Issuer     string `json:"issuer"`
	Contains   string `json:"contains"`
	CategoryID int64  `json:"category_id"`
	Category   string `json:"category"`
}

// migrateCategories adds the category tables and columns, and the default
// categories the first time.
func (s *Store) migrateCategories() error {
	if _, err := s.DB.Exec(`CREATE TABLE IF NOT EXISTS categories(
  id INTEGER PRIMARY KEY, name TEXT NOT NULL UNIQUE COLLATE NOCASE);
CREATE TABLE IF NOT EXISTS category_rules(
  id INTEGER PRIMARY KEY, issuer TEXT NOT NULL, contains TEXT NOT NULL,
  category_id INTEGER NOT NULL REFERENCES categories(id) ON DELETE CASCADE)`); err != nil {
		return err
	}
	for _, c := range [][2]string{
		{"category_id", "INTEGER REFERENCES categories(id) ON DELETE SET NULL"},
		{"category_source", "TEXT NOT NULL DEFAULT ''"},
	} {
		if err := s.addColumn("transactions", c[0], c[1]); err != nil {
			return err
		}
	}
	seeded, err := s.Setting("categories-seeded")
	if err != nil || seeded == "1" {
		return err
	}
	for _, name := range DefaultCategories {
		if _, err = s.DB.Exec("INSERT OR IGNORE INTO categories(name) VALUES(?)", name); err != nil {
			return err
		}
	}
	return s.SetSetting("categories-seeded", "1")
}

// Categories lists your categories by name.
func (s *Store) Categories() ([]Category, error) {
	rows, err := s.DB.Query("SELECT id,name FROM categories ORDER BY name COLLATE NOCASE")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Category{}
	for rows.Next() {
		var c Category
		if err = rows.Scan(&c.ID, &c.Name); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func categoryName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if name == "" || len(name) > 40 {
		return "", errors.New("A category needs a name (up to 40 characters)")
	}
	return name, nil
}

// SaveCategory adds a category (id 0) or renames one.
func (s *Store) SaveCategory(id int64, name string) (Category, error) {
	name, err := categoryName(name)
	if err != nil {
		return Category{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var taken int64
	if err = s.DB.QueryRow("SELECT id FROM categories WHERE name=? COLLATE NOCASE", name).Scan(&taken); err == nil && taken != id {
		return Category{}, errors.New("There's already a category with that name")
	} else if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Category{}, err
	}
	if id == 0 {
		res, err := s.DB.Exec("INSERT INTO categories(name) VALUES(?)", name)
		if err != nil {
			return Category{}, err
		}
		id, err = res.LastInsertId()
		return Category{ID: id, Name: name}, err
	}
	res, err := s.DB.Exec("UPDATE categories SET name=? WHERE id=?", name, id)
	if err != nil {
		return Category{}, err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return Category{}, sql.ErrNoRows
	}
	return Category{ID: id, Name: name}, nil
}

// DeleteCategory removes a category and its rules; its transactions become
// uncategorised, unless another rule now fits them.
func (s *Store) DeleteCategory(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.DB.Exec("UPDATE transactions SET category_id=NULL,category_source='' WHERE category_id=?", id); err != nil {
		return err
	}
	if _, err := s.DB.Exec("DELETE FROM category_rules WHERE category_id=?", id); err != nil {
		return err
	}
	res, err := s.DB.Exec("DELETE FROM categories WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return s.findCategories()
}

// SetCategory puts a transaction in a category by hand, or with category 0
// hands it back to the rules.
func (s *Store) SetCategory(id, category int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if category != 0 {
		if err := s.DB.QueryRow("SELECT id FROM categories WHERE id=?", category).Scan(&category); err != nil {
			return err
		}
	}
	var res sql.Result
	var err error
	if category == 0 {
		res, err = s.DB.Exec("UPDATE transactions SET category_id=NULL,category_source='' WHERE id=?", id)
	} else {
		res, err = s.DB.Exec("UPDATE transactions SET category_id=?,category_source='manual' WHERE id=?", category, id)
	}
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return s.findCategories()
}

// CategoryRules lists the rules, most specific first: the order they're
// tried in.
func (s *Store) CategoryRules() ([]CategoryRule, error) {
	rows, err := s.DB.Query(`SELECT r.id,r.issuer,r.contains,r.category_id,c.name
FROM category_rules r JOIN categories c ON c.id=r.category_id
ORDER BY length(r.contains) DESC, r.issuer!='' DESC, r.id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CategoryRule{}
	for rows.Next() {
		var r CategoryRule
		if err = rows.Scan(&r.ID, &r.Issuer, &r.Contains, &r.CategoryID, &r.Category); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveCategoryRule adds a rule, or replaces the category of one with the
// same text and issuer, and applies it.
func (s *Store) SaveCategoryRule(r CategoryRule) (CategoryRule, error) {
	r.Contains = strings.Join(strings.Fields(r.Contains), " ")
	r.Issuer = strings.TrimSpace(r.Issuer)
	if len(r.Contains) < 3 || len(r.Contains) > 200 {
		return r, errors.New("A rule needs 3 to 200 characters of the description to look for")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.DB.QueryRow("SELECT name FROM categories WHERE id=?", r.CategoryID).Scan(&r.Category); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return r, errors.New("Choose a category")
		}
		return r, err
	}
	err := s.DB.QueryRow("SELECT id FROM category_rules WHERE contains=? COLLATE NOCASE AND issuer=? COLLATE NOCASE",
		r.Contains, r.Issuer).Scan(&r.ID)
	switch {
	case err == nil:
		_, err = s.DB.Exec("UPDATE category_rules SET category_id=? WHERE id=?", r.CategoryID, r.ID)
	case errors.Is(err, sql.ErrNoRows):
		var res sql.Result
		if res, err = s.DB.Exec("INSERT INTO category_rules(issuer,contains,category_id) VALUES(?,?,?)",
			r.Issuer, r.Contains, r.CategoryID); err == nil {
			r.ID, err = res.LastInsertId()
		}
	}
	if err != nil {
		return r, err
	}
	return r, s.findCategories()
}

// DeleteCategoryRule removes a rule; what it categorised is worked out
// again.
func (s *Store) DeleteCategoryRule(id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	res, err := s.DB.Exec("DELETE FROM category_rules WHERE id=?", id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return s.findCategories()
}

// categoryFor is the category of the first rule that fits.
func categoryFor(rules []CategoryRule, issuer, merchant string) int64 {
	merchant = strings.ToLower(merchant)
	for _, r := range rules {
		if (r.Issuer == "" || strings.EqualFold(r.Issuer, strings.TrimSpace(issuer))) &&
			strings.Contains(merchant, strings.ToLower(r.Contains)) {
			return r.CategoryID
		}
	}
	return 0
}

// findCategories applies the rules to every transaction not categorised
// by hand. The caller holds s.mu, with no transaction open.
func (s *Store) findCategories() error {
	rules, err := s.CategoryRules()
	if err != nil {
		return err
	}
	rows, err := s.DB.Query("SELECT id,issuer,merchant,coalesce(category_id,0) FROM transactions WHERE category_source!='manual'")
	if err != nil {
		return err
	}
	type change struct{ id, category int64 }
	changes := []change{}
	for rows.Next() {
		var id, have int64
		var issuer, merchant string
		if err = rows.Scan(&id, &issuer, &merchant, &have); err != nil {
			rows.Close()
			return err
		}
		if want := categoryFor(rules, issuer, merchant); want != have {
			changes = append(changes, change{id, want})
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return err
	}
	for _, c := range changes {
		if c.category == 0 {
			_, err = s.DB.Exec("UPDATE transactions SET category_id=NULL,category_source='' WHERE id=?", c.id)
		} else {
			_, err = s.DB.Exec("UPDATE transactions SET category_id=?,category_source='rule' WHERE id=?", c.category, c.id)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// CategoryTotal is one category's spending in a month: debits that aren't
// transfers or dismissed. Category 0 is the uncategorised.
type CategoryTotal struct {
	CategoryID int64  `json:"category_id"`
	Category   string `json:"category"`
	Currency   string `json:"currency"`
	Amount     int64  `json:"amount"`
	Count      int    `json:"count"`
}

// MonthCategories is month's (YYYY-MM) spending per category, largest first.
func (s *Store) MonthCategories(month string) ([]CategoryTotal, error) {
	rows, err := s.DB.Query(`SELECT coalesce(c.id,0),coalesce(c.name,''),t.currency,sum(t.amount),count(*)
FROM transactions t LEFT JOIN categories c ON c.id=t.category_id
WHERE substr(t.date,1,7)=? AND t.status!='dismissed' AND t.transfer='' AND t.direction='debit'
GROUP BY 1,3 ORDER BY 4 DESC`, month)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []CategoryTotal{}
	for rows.Next() {
		var c CategoryTotal
		if err = rows.Scan(&c.CategoryID, &c.Category, &c.Currency, &c.Amount, &c.Count); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
