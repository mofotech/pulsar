package main

import (
	"github.com/spf13/cobra"
)

func newProjectCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "projects", Short: "Manage projects and users"}
	cmd.AddCommand(
		projectListCmd(),
		projectGetCmd(),
		projectCreateCmd(),
		userListCmd(),
		userCreateCmd(),
	)
	return cmd
}

func projectListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List projects",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/projects"), tok, nil)
			if err != nil {
				return err
			}
			if globalJSON {
				printJSON(data)
				return nil
			}
			items, err := unmarshalSlice(data)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, it := range items {
				rows = append(rows, []string{field(it, "id"), field(it, "name"), field(it, "status"), field(it, "created_at")})
			}
			printTable("ID|NAME|STATUS|CREATED AT", rows)
			return nil
		},
	}
}

func projectGetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "get <id>", Short: "Show project details", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/projects/"+args[0]), tok, nil)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
}

func projectCreateCmd() *cobra.Command {
	var name string
	cmd := &cobra.Command{
		Use: "create", Short: "Create a project",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("POST", apiURL(ep, "/v1/projects"), tok, map[string]string{"name": name})
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Project name")
	cmd.MarkFlagRequired("name") //nolint:errcheck
	return cmd
}

func userListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "users", Short: "List users",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/users"), tok, nil)
			if err != nil {
				return err
			}
			if globalJSON {
				printJSON(data)
				return nil
			}
			items, err := unmarshalSlice(data)
			if err != nil {
				return err
			}
			var rows [][]string
			for _, it := range items {
				rows = append(rows, []string{field(it, "id"), field(it, "email"), field(it, "role"), field(it, "project_id")})
			}
			printTable("ID|EMAIL|ROLE|PROJECT", rows)
			return nil
		},
	}
}

func userCreateCmd() *cobra.Command {
	var email, role string
	cmd := &cobra.Command{
		Use: "create-user", Short: "Create a user",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("POST", apiURL(ep, "/v1/users"), tok,
				map[string]string{"email": email, "role": role})
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&email, "email", "", "User email")
	cmd.Flags().StringVar(&role, "role", "member", "Role (admin|member)")
	cmd.MarkFlagRequired("email") //nolint:errcheck
	return cmd
}

