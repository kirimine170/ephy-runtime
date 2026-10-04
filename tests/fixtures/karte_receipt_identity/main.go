// Synthetic subprocess driver for tests/test_karte_receipt_identity_integration.py.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"karte/internal/ephyoutbox"
)

func main() {
	if len(os.Args) != 4 {
		fail(fmt.Errorf("expected read|write synthetic-root candidate-id"))
	}
	marker, err := os.ReadFile(filepath.Join(os.Args[2], ".receipt-identity-fixture"))
	if err != nil || string(marker) != "synthetic\n" {
		fail(fmt.Errorf("synthetic fixture marker is required"))
	}
	store, err := ephyoutbox.NewStore(os.Args[2])
	if err != nil {
		fail(err)
	}
	if os.Args[1] == "write" {
		var receipt ephyoutbox.Receipt
		if err := json.NewDecoder(os.Stdin).Decode(&receipt); err != nil {
			fail(err)
		}
		if receipt.CandidateID != os.Args[3] {
			fail(fmt.Errorf("driver input identity mismatch"))
		}
		if err := store.WriteReceipt(receipt); err != nil {
			fail(err)
		}
	} else if os.Args[1] != "read" {
		fail(fmt.Errorf("unsupported driver command"))
	}
	receipt, err := store.ReadReceipt(os.Args[3])
	if err != nil {
		fail(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(receipt); err != nil {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, err)
	os.Exit(3)
}
