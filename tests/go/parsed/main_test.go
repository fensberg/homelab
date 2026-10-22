package parsed

import (
	"os"
	"testing"
)

// These tests run git in trees of their own, and start as the guards do.
func TestMain(m *testing.M) {
	os.Exit(Main(m))
}
