package channels

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// local runs the program on this machine, as the service does on the host.
type local struct{}

func (local) Run(ctx context.Context, name string, args ...string) error {
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		if said := strings.TrimSpace(string(out[:min(len(out), 200)])); said != "" {
			return fmt.Errorf("%w: %s", err, said)
		}
	}
	return err
}
