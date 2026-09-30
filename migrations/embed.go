// Package migrations embeds the SQL migrations (golang-migrate format:
// NNNNNN_name.up.sql / NNNNNN_name.down.sql) into the binary.
//
// Create a new pair with: make migrate-create name=add_something
package migrations

import "embed"

//go:embed *.sql
var FS embed.FS
