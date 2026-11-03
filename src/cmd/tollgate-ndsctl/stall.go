package main

import (
	"os"
	"strconv"
	"time"
)

// stallEnv widens the read-do-write window inside authLocked so the concurrency
// evidence can reproduce the duplicate-rule race deterministically instead of
// hoping for a lucky interleave. It is a no-op unless the operator sets
// TOLLGATE_NFT_TEST_STALL_MS; production never sets it.
const stallEnv = "TOLLGATE_NFT_TEST_STALL_MS"

func testHookStall() {
	v := os.Getenv(stallEnv)
	if v == "" {
		return
	}
	ms, err := strconv.Atoi(v)
	if err != nil || ms <= 0 {
		return
	}
	time.Sleep(time.Duration(ms) * time.Millisecond)
}
