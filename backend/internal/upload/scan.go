package upload

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/platform"
)

// Scanner reports whether data is malware-free. A nil error means clean.
// Implementations must fail closed: if the scan could not complete, return an
// error rather than nil (IDEA-005: nothing becomes visible unscanned).
type Scanner interface {
	Scan(ctx context.Context, data []byte) error
}

// ClamAV talks to clamd using the INSTREAM command over TCP (ADR 0010).
type ClamAV struct {
	Addr    string
	Timeout time.Duration // whole-scan budget; defaults to 30 s
}

// chunkSize is well below clamd's StreamMaxLength per chunk limit; clamd only
// requires that the total stays under StreamMaxLength (default 25 MB > 5 MB).
const chunkSize = 64 << 10

// Ping checks that clamd is up and has loaded its signatures.
func (c ClamAV) Ping(ctx context.Context) error {
	resp, err := c.roundTrip(ctx, "zPING\x00", nil)
	if err != nil {
		return err
	}
	if resp != "PONG" {
		return fmt.Errorf("clamd ping: unexpected reply %q", resp)
	}
	return nil
}

func (c ClamAV) Scan(ctx context.Context, data []byte) error {
	resp, err := c.roundTrip(ctx, "zINSTREAM\x00", data)
	if err != nil {
		return scanUnavailable(err)
	}
	switch {
	case strings.HasSuffix(resp, " FOUND"):
		// "stream: Eicar-Test-Signature FOUND". The signature name is kept out
		// of the client-facing detail; callers may log the wrapped error.
		return fmt.Errorf("%w: %s", errMalware, resp)
	case strings.HasSuffix(resp, " OK"):
		return nil
	default:
		// "... ERROR" (e.g. size limit exceeded) or anything unexpected.
		return scanUnavailable(fmt.Errorf("clamd reply %q", resp))
	}
}

var errMalware = reject(http.StatusUnprocessableEntity, CodeMalware, "The file was rejected by the security scan.")

// IsMalware reports whether err means the scanner found malware.
func IsMalware(err error) bool {
	var p *platform.Problem
	return errors.As(err, &p) && p.Code == CodeMalware
}

func scanUnavailable(cause error) error {
	// Retryable, and distinct from "infected" so ops can alert on it.
	return fmt.Errorf("%w: %v", reject(http.StatusServiceUnavailable, CodeScanUnavailable, "The security scan is temporarily unavailable. Please retry."), cause)
}

func (c ClamAV) roundTrip(ctx context.Context, cmd string, data []byte) (string, error) {
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(ctx, "tcp", c.Addr)
	if err != nil {
		return "", err
	}
	defer conn.Close()
	// One deadline for the whole exchange; also unblocks on ctx expiry.
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()

	if _, err := conn.Write([]byte(cmd)); err != nil {
		return "", err
	}
	if data != nil {
		var hdr [4]byte
		for off := 0; off < len(data); off += chunkSize {
			end := min(off+chunkSize, len(data))
			binary.BigEndian.PutUint32(hdr[:], uint32(end-off))
			if _, err := conn.Write(hdr[:]); err != nil {
				return "", err
			}
			if _, err := conn.Write(data[off:end]); err != nil {
				return "", err
			}
		}
		// Zero-length chunk terminates the stream.
		binary.BigEndian.PutUint32(hdr[:], 0)
		if _, err := conn.Write(hdr[:]); err != nil {
			return "", err
		}
	}

	// With the "z" prefix clamd terminates the reply with NUL.
	var buf []byte
	one := make([]byte, 512)
	for {
		n, err := conn.Read(one)
		buf = append(buf, one[:n]...)
		if i := indexNUL(buf); i >= 0 {
			return strings.TrimSpace(string(buf[:i])), nil
		}
		if err != nil {
			return "", fmt.Errorf("reading clamd reply: %w", err)
		}
		if len(buf) > 4096 {
			return "", errors.New("clamd reply too long")
		}
	}
}

func indexNUL(b []byte) int {
	for i, c := range b {
		if c == 0 {
			return i
		}
	}
	return -1
}
