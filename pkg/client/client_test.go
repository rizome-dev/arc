package client

import "testing"

func TestNew(t *testing.T) {
    client := New()
    if client == nil {
        t.Error("New() returned nil")
    }
}
