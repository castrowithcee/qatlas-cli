// Package endpoints holds the fixed AWS endpoint table, the mapping of service model names to tool-ID
// segments, and the validation of region names. It is data plus lookups: no host ever comes from an
// argument or from configuration.
package endpoints

import (
	"errors"
	"regexp"
	"strings"
)

// Region errors carry no input, so a rejected value is never echoed.
var (
	ErrRegionInvalid   = errors.New("not a valid AWS region name")
	ErrRegionPartition = errors.New("only regions of the aws partition are supported")
)

// regionForm matches the commercial partition's names: two letters, one direction word, a number, such as
// us-east-1, eu-central-1 or ap-southeast-2. Partitions with longer names (aws-us-gov, aws-iso*, aws-eusc)
// do not fit it; cn- does and is refused by prefix below.
var regionForm = regexp.MustCompile(`^[a-z]{2}-[a-z]+-[0-9]{1,2}$`)

// foreignPrefixes name the regions of the partitions aws-cn and aws-us-gov. They are checked first so the
// refusal says why.
var foreignPrefixes = []string{"cn-", "us-gov-"}

// ValidRegion reports whether name is a region of the aws partition by form. It does not know which regions
// exist or are enabled for an account; the account's allowlist and AWS decide that.
func ValidRegion(name string) error {
	for _, prefix := range foreignPrefixes {
		if strings.HasPrefix(name, prefix) {
			return ErrRegionPartition
		}
	}
	if len(name) > 32 || !regionForm.MatchString(name) {
		return ErrRegionInvalid
	}
	return nil
}
