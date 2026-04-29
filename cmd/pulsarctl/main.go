package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	globalEndpoint string
	globalToken    string
	globalJSON     bool
)

var rootCmd = &cobra.Command{
	Use:   "pulsarctl",
	Short: "CLI for the Pulsar compute orchestration platform",
}

func init() {
	rootCmd.PersistentFlags().StringVar(&globalEndpoint, "endpoint", "", "Pulsar API endpoint (e.g. http://10.11.3.190:8080)")
	rootCmd.PersistentFlags().StringVar(&globalToken, "token", "", "Bearer token (overrides stored credentials)")
	rootCmd.PersistentFlags().BoolVar(&globalJSON, "json", false, "Output raw JSON")

	rootCmd.AddCommand(
		newLoginCmd(),
		newLogoutCmd(),
		newComputeCmd(),
		newNetworkCmd(),
		newStorageCmd(),
		newProjectCmd(),
		newImagesCmd(),
	)
}

// ─── Auth ─────────────────────────────────────────────────────────────────────

func newLoginCmd() *cobra.Command {
	var email, password, endpoint string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate and store credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			if endpoint == "" {
				return fmt.Errorf("--endpoint is required")
			}
			data, err := do("POST", apiURL(endpoint, "/v1/auth/tokens"), "", map[string]string{
				"email":    email,
				"password": password,
			})
			if err != nil {
				return err
			}
			var resp struct {
				Token     string `json:"token"`
				ExpiresAt string `json:"expires_at"`
			}
			if err := json.Unmarshal(data, &resp); err != nil {
				return err
			}
			if err := saveCreds(&credentials{Endpoint: endpoint, Token: resp.Token}); err != nil {
				return err
			}
			fmt.Printf("Logged in. Token expires at %s\n", resp.ExpiresAt)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "Email address")
	cmd.Flags().StringVar(&password, "password", "", "Password")
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "API endpoint")
	cmd.MarkFlagRequired("email")    //nolint:errcheck
	cmd.MarkFlagRequired("password") //nolint:errcheck
	cmd.MarkFlagRequired("endpoint") //nolint:errcheck

	cmd.AddCommand(newLoginSSOCmd(), newLoginTokenCmd())
	return cmd
}

// newLoginTokenCmd stores a personal access token (PAT) as the active credentials.
// Usage: pulsarctl login token --endpoint <url> --token <pat_...>
func newLoginTokenCmd() *cobra.Command {
	var endpoint, token string
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Store a personal access token as credentials",
		Long: `Save a personal access token (PAT) so all subsequent pulsarctl commands
use it without needing to pass --token every time.

Create a PAT in the web UI under Identity → Access Tokens, then run:

  pulsarctl login token --endpoint http://10.11.3.190:8080 --token pat_...`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if endpoint == "" {
				return fmt.Errorf("--endpoint is required")
			}
			if token == "" {
				return fmt.Errorf("--token is required")
			}
			// Verify the token works before saving it.
			if _, err := do("GET", apiURL(endpoint, "/v1/projects"), token, nil); err != nil {
				return fmt.Errorf("token validation failed: %w", err)
			}
			if err := saveCreds(&credentials{Endpoint: endpoint, Token: token}); err != nil {
				return err
			}
			fmt.Println("Credentials saved. You can now use pulsarctl commands without --token.")
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "API endpoint (e.g. http://10.11.3.190:8080)")
	cmd.Flags().StringVar(&token, "token", "", "Personal access token (pat_...)")
	cmd.MarkFlagRequired("endpoint") //nolint:errcheck
	cmd.MarkFlagRequired("token")    //nolint:errcheck
	return cmd
}

func newLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove stored credentials",
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := deleteCreds(); err != nil && !os.IsNotExist(err) {
				return err
			}
			fmt.Println("Logged out.")
			return nil
		},
	}
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
