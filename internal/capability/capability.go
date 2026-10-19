package capability

import (
	"context"
	"fmt"
	"github.com/maxbeizer/gh-babysit/internal/process"
	"regexp"
	"strconv"
	"strings"
)

type Version struct{ Major, Minor, Patch int }

var versionPattern = regexp.MustCompile(`(?:^|[^0-9])(\d+)\.(\d+)\.(\d+)(?:[^0-9]|$)`)

func ParseVersion(s string) (Version, error) {
	m := versionPattern.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("version not found in %q", strings.TrimSpace(s))
	}
	a, _ := strconv.Atoi(m[1])
	b, _ := strconv.Atoi(m[2])
	c, _ := strconv.Atoi(m[3])
	return Version{a, b, c}, nil
}
func (v Version) AtLeast(min Version) bool {
	if v.Major != min.Major {
		return v.Major > min.Major
	}
	if v.Minor != min.Minor {
		return v.Minor > min.Minor
	}
	return v.Patch >= min.Patch
}
func Require(ctx context.Context, runner process.Runner, executable string, minimum Version, capabilityArgs ...string) error {
	r, err := runner.Run(ctx, process.Request{Name: executable, Args: []string{"--version"}})
	if err != nil {
		return fmt.Errorf("%s executable/version check: %w", executable, err)
	}
	v, err := ParseVersion(string(append(r.Stdout, r.Stderr...)))
	if err != nil {
		return fmt.Errorf("%s: %w", executable, err)
	}
	if !v.AtLeast(minimum) {
		return fmt.Errorf("%s %d.%d.%d is unsupported; require %d.%d.%d or newer", executable, v.Major, v.Minor, v.Patch, minimum.Major, minimum.Minor, minimum.Patch)
	}
	if len(capabilityArgs) > 0 {
		if _, err = runner.Run(ctx, process.Request{Name: executable, Args: capabilityArgs}); err != nil {
			return fmt.Errorf("%s lacks required capability %q: %w", executable, strings.Join(capabilityArgs, " "), err)
		}
	}
	return nil
}
