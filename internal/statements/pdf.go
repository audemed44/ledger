package statements

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// MaxPDFSize bounds the PDFs Ledger will extract.
const MaxPDFSize = 25 << 20

// Extract returns the text of a local PDF, decrypting it with password if
// it's protected.
func Extract(ctx context.Context, path, password string) (string, error) {
	text, _, err := ExtractWithPasswords(ctx, path, []string{password}, 0)
	return text, err
}

// ExtractWithPasswords returns the text of a PDF and which password slot
// opened it (0 when it isn't encrypted). Slot zero tries every configured
// password; a positive slot tries only that one.
//
// Passwords go to qpdf over stdin, never the argument list, and the
// decrypted copy lives in a private temporary directory until extraction
// ends.
func ExtractWithPasswords(ctx context.Context, path string, passwords []string, slot int) (string, int, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > MaxPDFSize {
		return "", 0, errors.New("PDF must be a regular file below 25 MiB")
	}
	if slot < 0 || slot > len(passwords) || (slot > 0 && passwords[slot-1] == "") {
		return "", 0, errors.New("Selected password slot is not configured; update LEDGER_PDF_PASSWORDS or choose automatic")
	}
	if len(passwords) > MaxPasswords {
		return "", 0, fmt.Errorf("LEDGER_PDF_PASSWORDS supports at most %d password slots", MaxPasswords)
	}
	for _, binary := range []string{"qpdf", "pdftotext"} {
		if _, err := exec.LookPath(binary); err != nil {
			return "", 0, fmt.Errorf("PDF tools unavailable: %s is not installed", binary)
		}
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	dir, err := os.MkdirTemp("", "ledger-pdf-*")
	if err != nil {
		return "", 0, err
	}
	defer os.RemoveAll(dir)
	decoded := filepath.Join(dir, "statement.pdf")

	slots := []int{0}
	if slot > 0 {
		slots = append(slots, slot)
	} else {
		for i, p := range passwords {
			if p != "" {
				slots = append(slots, i+1)
			}
		}
	}
	matched := -1
	for _, candidate := range slots {
		if ctx.Err() != nil {
			return "", 0, errors.New("PDF extraction was cancelled or timed out")
		}
		password := ""
		if candidate > 0 {
			password = passwords[candidate-1]
		}
		cmd := exec.CommandContext(ctx, "qpdf", "--password-file=-", "--decrypt", path, decoded)
		cmd.Stdin = strings.NewReader(password + "\n")
		err = cmd.Run()
		// Exit code 3 is success with warnings.
		var exit *exec.ExitError
		if err == nil || (errors.As(err, &exit) && exit.ExitCode() == 3) {
			matched = candidate
			break
		}
	}
	if matched < 0 {
		if len(slots) == 1 {
			return "", 0, errors.New("Could not open PDF. Configure LEDGER_PDF_PASSWORDS for encrypted statements, or check that the PDF is valid")
		}
		return "", 0, errors.New("Could not open PDF with the selected password(s). Check LEDGER_PDF_PASSWORDS and that the PDF is valid")
	}

	var output limitedBuffer
	cmd := exec.CommandContext(ctx, "pdftotext", "-layout", decoded, "-")
	cmd.Stdout = &output
	if err = cmd.Run(); err != nil {
		return "", 0, errors.New("PDF text extraction failed; the document may be damaged or too large")
	}
	return output.String(), matched, nil
}

// limitedBuffer refuses to grow past 4 MiB of extracted text.
type limitedBuffer struct{ bytes.Buffer }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > 4<<20 {
		return 0, errors.New("extracted text exceeds 4 MiB")
	}
	return b.Buffer.Write(p)
}
