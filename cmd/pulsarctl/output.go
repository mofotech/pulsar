package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
)

func printTable(header string, rows [][]string) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 3, ' ', 0)
	cols := strings.Split(header, "|")
	fmt.Fprintln(w, strings.Join(cols, "\t"))
	fmt.Fprintln(w, strings.Repeat("-\t", len(cols)))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
	w.Flush()
}

func printJSON(data json.RawMessage) {
	var v interface{}
	if err := json.Unmarshal(data, &v); err != nil {
		fmt.Println(string(data))
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	enc.Encode(v) //nolint:errcheck
}

func unmarshalSlice(data json.RawMessage) ([]json.RawMessage, error) {
	var items []json.RawMessage
	if err := json.Unmarshal(data, &items); err != nil {
		return nil, err
	}
	return items, nil
}

// field extracts a string field from a raw JSON object.
func field(raw json.RawMessage, key string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	v, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		// not a string — return raw value without quotes
		return strings.Trim(string(v), `"`)
	}
	return s
}

// fieldArr extracts a string field from the first element of a JSON array field.
// e.g. fieldArr(raw, "fixed_ips", "ip_address") → port's first IP address.
func fieldArr(raw json.RawMessage, arrayKey, subKey string) string {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return ""
	}
	arrRaw, ok := m[arrayKey]
	if !ok {
		return ""
	}
	var arr []json.RawMessage
	if err := json.Unmarshal(arrRaw, &arr); err != nil || len(arr) == 0 {
		return ""
	}
	return field(arr[0], subKey)
}
