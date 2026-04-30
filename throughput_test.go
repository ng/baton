package main

import (
	"testing"
)

func TestParseNetDev(t *testing.T) {
	sample := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 12345678   12345    0    0    0     0          0         0 12345678   12345    0    0    0     0       0          0
  eth0: 98765432   54321    0    0    0     0          0         0 87654321   43210    0    0    0     0       0          0
  ens4: 11111111   11111    0    0    0     0          0         0 22222222   22222    0    0    0     0       0          0`

	rx, tx := parseNetDev(sample)

	// eth0 + ens4 (lo excluded)
	expectedRx := uint64(98765432 + 11111111)
	expectedTx := uint64(87654321 + 22222222)

	if rx != expectedRx {
		t.Errorf("rx = %d, want %d", rx, expectedRx)
	}
	if tx != expectedTx {
		t.Errorf("tx = %d, want %d", tx, expectedTx)
	}
}

func TestParseNetDevEmpty(t *testing.T) {
	rx, tx := parseNetDev("")
	if rx != 0 || tx != 0 {
		t.Errorf("expected (0, 0) for empty input, got (%d, %d)", rx, tx)
	}
}

func TestParseNetDevLoOnly(t *testing.T) {
	sample := `Inter-|   Receive                                                |  Transmit
 face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed
    lo: 12345678   12345    0    0    0     0          0         0 12345678   12345    0    0    0     0       0          0`

	rx, tx := parseNetDev(sample)
	if rx != 0 || tx != 0 {
		t.Errorf("expected (0, 0) with lo only, got (%d, %d)", rx, tx)
	}
}
