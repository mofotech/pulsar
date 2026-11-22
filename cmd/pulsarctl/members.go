package main

import (
	"fmt"

	"github.com/spf13/cobra"
)

func projectMembersCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "members <project-id>",
		Short: "List a project's members and their roles",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/projects/"+args[0]+"/members"), tok, nil)
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
				rows = append(rows, []string{field(it, "user_id"), field(it, "email"), field(it, "role")})
			}
			printTable("USER ID|EMAIL|ROLE", rows)
			return nil
		},
	}
}

func projectSetMemberCmd() *cobra.Command {
	var role string
	cmd := &cobra.Command{
		Use:   "set-member <project-id> <user-id>",
		Short: "Add a user to a project or change their role (project/org/platform admin)",
		Long: "Roles:\n" +
			"  reader  view the project's resources\n" +
			"  member  create, change and delete resources\n" +
			"  admin   also manage the project's members",
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch role {
			case "reader", "member", "admin":
			default:
				return fmt.Errorf("--role must be reader, member or admin")
			}
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("PUT", apiURL(ep, "/v1/projects/"+args[0]+"/members/"+args[1]), tok,
				map[string]string{"role": role})
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&role, "role", "member", "reader | member | admin")
	return cmd
}

func projectRemoveMemberCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "remove-member <project-id> <user-id>",
		Short: "Remove a user from a project (project/org/platform admin)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			if _, err := do("DELETE", apiURL(ep, "/v1/projects/"+args[0]+"/members/"+args[1]), tok, nil); err != nil {
				return err
			}
			fmt.Println("removed")
			return nil
		},
	}
}
