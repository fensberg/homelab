package run

import "os/exec"

// Ping shells out to the system ping binary rather than crafting raw ICMP,
// which would need elevated capabilities this program has no other reason
// to hold. Matches Test-Connection's two-probe check.
func Ping(host string) bool {
	return exec.Command("ping", "-c", "2", "-W", "2", host).Run() == nil
}
