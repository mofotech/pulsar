package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
)

func newImagesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "images", Short: "Manage images"}
	cmd.AddCommand(
		imageListCmd(),
		imageGetCmd(),
		imageUploadCmd(),
		imageRegisterCmd(),
		imageDeleteCmd(),
	)
	return cmd
}

func imageListCmd() *cobra.Command {
	return &cobra.Command{
		Use: "list", Short: "List images",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/images"), tok, nil)
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
				rows = append(rows, []string{
					field(it, "id"), field(it, "name"), field(it, "format"),
					field(it, "status"), field(it, "size_bytes"),
				})
			}
			printTable("ID|NAME|FORMAT|STATUS|SIZE (bytes)", rows)
			return nil
		},
	}
}

func imageGetCmd() *cobra.Command {
	return &cobra.Command{
		Use: "get <id>", Short: "Show image details", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("GET", apiURL(ep, "/v1/images/"+args[0]), tok, nil)
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
}

func imageUploadCmd() *cobra.Command {
	var name, format, filePath string
	cmd := &cobra.Command{
		Use:   "upload",
		Short: "Upload an image file",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}

			f, err := os.Open(filePath)
			if err != nil {
				return fmt.Errorf("open %s: %w", filePath, err)
			}
			defer f.Close()

			fi, err := f.Stat()
			if err != nil {
				return err
			}

			// Stream multipart body via io.Pipe — no full-file buffering
			pr, pw := io.Pipe()
			mw := multipart.NewWriter(pw)
			go func() {
				defer pw.Close()
				mw.WriteField("name", name)     //nolint:errcheck
				mw.WriteField("format", format) //nolint:errcheck
				part, err := mw.CreateFormFile("file", filepath.Base(filePath))
				if err != nil {
					pw.CloseWithError(err)
					return
				}
				if _, err := io.Copy(part, f); err != nil {
					pw.CloseWithError(err)
					return
				}
				mw.Close() //nolint:errcheck
			}()

			req, err := http.NewRequestWithContext(context.Background(), "POST", apiURL(ep, "/v1/images"), pr)
			if err != nil {
				return err
			}
			req.Header.Set("Content-Type", mw.FormDataContentType())
			req.Header.Set("Authorization", "Bearer "+tok)
			req.ContentLength = -1 // streaming, unknown length
			// Show progress hint
			fmt.Printf("Uploading %s (%d bytes)...\n", filepath.Base(filePath), fi.Size())

			resp, err := httpClient.Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()

			body, _ := io.ReadAll(resp.Body)
			var env apiEnvelope
			if err := json.Unmarshal(body, &env); err != nil {
				return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
			}
			if env.Error != nil {
				return fmt.Errorf("%s: %s", env.Error.Code, env.Error.Message)
			}
			printJSON(env.Data)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Image name")
	cmd.Flags().StringVar(&format, "format", "qcow2", "Disk format (qcow2|raw|oci)")
	cmd.Flags().StringVar(&filePath, "file", "", "Path to image file")
	cmd.MarkFlagRequired("name") //nolint:errcheck
	cmd.MarkFlagRequired("file") //nolint:errcheck
	return cmd
}

func imageRegisterCmd() *cobra.Command {
	var name, format, url string
	cmd := &cobra.Command{
		Use:   "register",
		Short: "Register an external image by URL (agent downloads at first use)",
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			data, err := do("POST", apiURL(ep, "/v1/images"), tok, map[string]string{
				"name": name, "format": format, "url": url,
			})
			if err != nil {
				return err
			}
			printJSON(data)
			return nil
		},
	}
	cmd.Flags().StringVar(&name, "name", "", "Image name")
	cmd.Flags().StringVar(&format, "format", "qcow2", "Disk format (qcow2|raw|oci)")
	cmd.Flags().StringVar(&url, "url", "", "External image URL")
	cmd.MarkFlagRequired("name") //nolint:errcheck
	cmd.MarkFlagRequired("url")  //nolint:errcheck
	return cmd
}

func imageDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use: "delete <id>", Short: "Delete an image", Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ep, tok, err := resolve()
			if err != nil {
				return err
			}
			if _, err := do("DELETE", apiURL(ep, "/v1/images/"+args[0]), tok, nil); err != nil {
				return err
			}
			fmt.Printf("Image %s deleted.\n", args[0])
			return nil
		},
	}
}
