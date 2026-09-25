// Package cloudflare is the shape of an answer from the vendor's v4 API.
//
// Every response comes wrapped in the same envelope - whether it worked, the
// result, and paging - and both the contractor, which asks Cloudflare what
// exists before OpenTofu is asked to adopt it, and the api tier, which proves
// the live account still answers as the estate expects, decode it. So it is
// drawn once, here.
package cloudflare

// Answer is the v4 envelope around a result of type T.
type Answer[T any] struct {
	Success    bool `json:"success"`
	Result     T    `json:"result"`
	ResultInfo struct {
		TotalPages int `json:"total_pages"`
	} `json:"result_info"`
}
