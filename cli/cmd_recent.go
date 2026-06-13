package cli

import (
	"github.com/spf13/cobra"
)

func (a *App) recentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recent",
		Short: "List recent nLab page changes",
		RunE: func(cmd *cobra.Command, _ []string) error {
			n := a.effectiveLimit(20)
			a.progressf("fetching %d recent changes...", n)
			results, err := a.client.Recent(cmd.Context(), n)
			if err != nil {
				return mapFetchErr(err)
			}
			return a.renderOrEmpty(results, len(results))
		},
	}
	return cmd
}
