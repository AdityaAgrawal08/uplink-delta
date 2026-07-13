package main

import (
	"fmt"

	"github.com/gen2brain/beeep"
)

func notifyTransferComplete(filename string) {
	_ = beeep.Notify("Uplink-Delta", fmt.Sprintf("Transfer complete: %s", filename), "")
}

func notifyTransferFailed(filename string, err error) {
	_ = beeep.Notify("Uplink-Delta", fmt.Sprintf("Transfer failed: %s - %v", filename, err), "")
}
