package cmd

import (
	"github.com/spf13/cobra"

	"github.com/liqotech/liqo/pkg/liqoctl/install"
)

// installCmd represents the generateInstall command.
var installCmd = &cobra.Command{
	Use:   "install",
	Short: "Install Liqo on a selected cluster",
	Long:  `Install Liqo on a selected cluster.`,
	Run:   install.HandleInstallCommand,
}

func init() {
	rootCmd.AddCommand(installCmd)

	installCmd.Flags().StringP("provider", "p", "kubeadm", "The provider for the cluster")
}
