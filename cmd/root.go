package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "trafczar",
	Short: "Czar of Traffic analysers",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("Run trafzar --help to list all options")
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
