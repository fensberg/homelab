// Package facts is every fact in config/facts, as Go constants.
//
// A fact is a value more than one language here needs: OpenTofu reads the
// file, and Go, which may run where there is no checkout to read it from, is
// generated from it. The constants are written by go generate and never by
// hand; tests/go/repo holds every generated file to what its generator makes
// of the files it is made from.
package facts

//go:generate go run ./generate ../../../config/facts .
