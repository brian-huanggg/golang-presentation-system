// Command hashpw prints an Argon2id PHC hash of a password read from stdin (NFR-SEC-003).
// It serves the Vault admins.json file and engineer password resets (Architecture 5.4).
//
// The password is read from stdin only: a command-line argument would leak into shell
// history and the process list. It reuses auth's hasher so the output always matches
// what login verifies.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"gitlab.com/devvvv_dogg/esg-award-site-golang/backend/internal/auth"
)

// maxPasswordBytes mirrors auth's cap: Verify rejects longer passwords, so a hash of one
// would lock the account out.
const maxPasswordBytes = 128

func main() {
	if err := run(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "hashpw:", err)
		os.Exit(1)
	}
}

func run(in io.Reader, out io.Writer) error {
	// Cap the read so a stray pipe cannot fill memory; +2 leaves room for CRLF.
	line, err := bufio.NewReaderSize(io.LimitReader(in, maxPasswordBytes+2+1), 512).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("read password: %w", err)
	}
	// Strip only the line ending: spaces may be part of the password.
	pw := strings.TrimRight(line, "\r\n")
	if pw == "" {
		return errors.New("empty password on stdin")
	}
	if len(pw) > maxPasswordBytes {
		return fmt.Errorf("password longer than %d bytes", maxPasswordBytes)
	}
	phc, err := auth.NewArgon2idHasher().Hash(context.Background(), pw)
	if err != nil {
		return fmt.Errorf("hash: %w", err)
	}
	_, err = fmt.Fprintln(out, phc)
	return err
}
