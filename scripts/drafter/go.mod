module homelab/drafter

go 1.26

// The drafter is the one program here that takes a library to read a format
// with. It holds no key, decides nothing and changes nothing: it redraws
// what is declared in the form every other program reads, which is JSON,
// so that no program that holds power has to take a dependency to read its
// own estate.
require gopkg.in/yaml.v3 v3.0.1
