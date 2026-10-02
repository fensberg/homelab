// Package privatenet names the three private address ranges of RFC 1918.
//
// The estate lives in the first of them, and policies are written in terms of
// all three: what an application may reach is "the internet, less every
// private range", and which of the other two it may open is its own decision.
// The guards that hold those policies and an application's own tests name the
// same ranges, so they are written here once.
package privatenet

const (
	// Ten is 10.0.0.0/8: the range this whole estate is addressed out of.
	Ten = "10.0.0.0/8"
	// OneSevenTwo is 172.16.0.0/12: phone hotspots, hotels, corporate
	// networks, and container bridges.
	OneSevenTwo = "172.16.0.0/12"
	// OneNineTwo is 192.168.0.0/16: most home networks.
	OneNineTwo = "192.168.0.0/16"
)
