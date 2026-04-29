package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"time"

	"github.com/spf13/cobra"
)

func newLoginSSOCmd() *cobra.Command {
	var endpoint, email string
	cmd := &cobra.Command{
		Use:   "sso",
		Short: "Authenticate via an external identity provider (OIDC browser flow)",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Printf("Looking up identity providers for %s...\n", email)
			idps, err := lookupIDPs(endpoint, email)
			if err != nil {
				return fmt.Errorf("failed to look up IDPs: %w", err)
			}
			if len(idps) == 0 {
				return fmt.Errorf("no SSO providers configured for %s", email)
			}
			idp := idps[0]
			if len(idps) > 1 {
				for i, p := range idps {
					fmt.Printf("  [%d] %s\n", i+1, p.Name)
				}
			}
			fmt.Printf("Using provider: %s\n", idp.Name)

			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				return fmt.Errorf("failed to start local callback server: %w", err)
			}
			port := listener.Addr().(*net.TCPAddr).Port
			cliRedirect := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

			tokenCh := make(chan string, 1)
			errCh := make(chan error, 1)

			srv := &http.Server{
				Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					token := r.URL.Query().Get("token")
					expiresAt := r.URL.Query().Get("expires_at")
					if token == "" {
						errMsg := r.URL.Query().Get("error")
						w.Header().Set("Content-Type", "text/html")
						fmt.Fprint(w, htmlResult(false, errMsg))
						errCh <- fmt.Errorf("SSO error: %s", errMsg)
						return
					}
					w.Header().Set("Content-Type", "text/html")
					fmt.Fprint(w, htmlResult(true, expiresAt))
					tokenCh <- token
				}),
			}

			go func() {
				if serveErr := srv.Serve(listener); serveErr != nil && serveErr != http.ErrServerClosed {
					errCh <- serveErr
				}
			}()

			authorizeURL := fmt.Sprintf("%s/v1/auth/oidc/%s/authorize?cli_redirect=%s",
				endpoint, idp.Slug, url.QueryEscape(cliRedirect))
			fmt.Printf("\nOpening browser...\n%s\n\n", authorizeURL)
			openBrowser(authorizeURL)

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
			defer cancel()

			var receivedToken string
			select {
			case receivedToken = <-tokenCh:
			case receiveErr := <-errCh:
				_ = srv.Shutdown(context.Background())
				return receiveErr
			case <-ctx.Done():
				_ = srv.Shutdown(context.Background())
				return fmt.Errorf("timed out waiting for authentication (5 minutes)")
			}
			_ = srv.Shutdown(context.Background())

			if saveErr := saveCreds(&credentials{Endpoint: endpoint, Token: receivedToken}); saveErr != nil {
				return fmt.Errorf("failed to save credentials: %w", saveErr)
			}
			fmt.Println("Authenticated successfully. Credentials saved.")
			return nil
		},
	}
	cmd.Flags().StringVar(&endpoint, "endpoint", "", "Pulsar API endpoint")
	cmd.Flags().StringVar(&email, "email", "", "Your email address")
	cmd.MarkFlagRequired("endpoint") //nolint:errcheck
	cmd.MarkFlagRequired("email")    //nolint:errcheck
	return cmd
}

type idpEntry struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Slug string `json:"slug"`
}

func lookupIDPs(endpoint, email string) ([]idpEntry, error) {
	u := fmt.Sprintf("%s/v1/auth/idps?email=%s", endpoint, url.QueryEscape(email))
	data, err := do("GET", u, "", nil)
	if err != nil {
		return nil, err
	}
	var idps []idpEntry
	if err := json.Unmarshal(data, &idps); err != nil {
		return nil, fmt.Errorf("unexpected response: %w", err)
	}
	return idps, nil
}

func openBrowser(u string) {
	var name string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		name, args = "open", []string{u}
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", u}
	default:
		name, args = "xdg-open", []string{u}
	}
	_ = exec.Command(name, args...).Start()
}

func htmlResult(success bool, detail string) string {
	const style = "<style>body{font-family:sans-serif;display:flex;align-items:center;" +
		"justify-content:center;height:100vh;margin:0;background:#0f0f0f;color:#e2e8f0}" +
		".card{text-align:center;padding:2rem;border:1px solid #2d2d2d;border-radius:12px;background:#1a1a1a}" +
		"h2{margin-bottom:.5rem}p{color:#94a3b8;font-size:.9rem}</style>"
	if success {
		return "<!doctype html><html><head><title>Pulsar</title>" + style + "</head>" +
			"<body><div class=\"card\"><h2 style=\"color:#22c55e\">Authenticated</h2>" +
			"<p>You can close this tab and return to the terminal.</p></div></body></html>"
	}
	return "<!doctype html><html><head><title>Pulsar</title>" + style + "</head>" +
		"<body><div class=\"card\"><h2 style=\"color:#ef4444\">Authentication failed</h2>" +
		"<p>" + detail + "</p></div></body></html>"
}
