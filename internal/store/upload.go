package store

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"mime"
	"path/filepath"
	"strings"
	"time"
	"unicode"
)

// UploadSender is the sender of statements uploaded by hand, for automatic
// imports to match.
const UploadSender = "uploads@ledger.invalid"

// MaxUpload bounds an uploaded PDF, so its email stays under mail.MaxSize.
const MaxUpload = 18 << 20

// Upload files a PDF statement downloaded by hand (some banks only email a
// link) as an email from UploadSender, then processes it like mail: it's
// archived, imported if a statement parser's trigger fits, and otherwise
// waits in the inbox. The same file uploaded again is the same email.
func (s *Store) Upload(name string, pdf []byte) (Message, error) {
	if len(pdf) > MaxUpload {
		return Message{}, fmt.Errorf("PDF exceeds %d MiB", MaxUpload>>20)
	}
	if !bytes.HasPrefix(pdf, []byte("%PDF-")) {
		return Message{}, errors.New("That file isn't a PDF")
	}
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, filepath.Base(strings.ReplaceAll(name, `\`, "/")))
	if len([]rune(name)) > 150 {
		name = string([]rune(name)[:150])
	}
	if name == "" || name == "." || name == "/" {
		name = "statement.pdf"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".pdf") {
		name += ".pdf"
	}

	hash := sha256.Sum256(pdf)
	var b strings.Builder
	fmt.Fprintf(&b, "From: Ledger uploads <%s>\r\nSubject: %s\r\nDate: %s\r\nMessage-ID: <%s@upload.ledger.invalid>\r\n"+
		"MIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=ledger-upload\r\n\r\n"+
		"--ledger-upload\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nUploaded to Ledger by hand.\r\n"+
		"--ledger-upload\r\nContent-Type: application/pdf\r\nContent-Disposition: %s\r\n"+
		"Content-Transfer-Encoding: base64\r\n\r\n",
		UploadSender, mime.QEncoding.Encode("utf-8", "Uploaded statement: "+name),
		time.Now().Format(time.RFC1123Z), hex.EncodeToString(hash[:]),
		mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	encoded := base64.StdEncoding.EncodeToString(pdf)
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n--ledger-upload--\r\n")

	id, err := s.Ingest([]byte(b.String()))
	if err != nil {
		return Message{}, err
	}
	return s.Message(id)
}
