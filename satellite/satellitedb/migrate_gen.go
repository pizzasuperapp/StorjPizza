// Copyright (C) 2021 Storj Labs, Inc.
// See LICENSE for copying information.

//go:build ignore
// +build ignore

package main

import (
	"bytes"
	"fmt"
	"go/format"
	"io/ioutil"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"storj.io/private/dbutil/dbschema"
)

func main() {
	// find all postgres sql files
	matches, err := filepath.Glob("testdata/postgres.*")
	if err != nil {
		panic(err)
	}

	sort.Slice(matches, func(i, k int) bool {
		return parseTestdataVersion(matches[i]) < parseTestdataVersion(matches[k])
	})

	lastScriptFile := matches[len(matches)-1]
	version := parseTestdataVersion(lastScriptFile)
	if version < 0 {
		panic("invalid version " + lastScriptFile)
	}

	scriptData, err := ioutil.ReadFile(lastScriptFile)
	if err != nil {
		panic(err)
	}

	sections := dbschema.NewSections(string(scriptData))
	data := sections.LookupSection(dbschema.Main)

	var buffer bytes.Buffer
	fmt.Fprintf(&buffer, testMigrationFormat, version, data)

	formatted, err := format.Source(buffer.Bytes())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s\n", formatted)
		panic(err)
	}

	err = ioutil.WriteFile("migratez.go", formatted, 0755)
	if err != nil {
		panic(err)
	}
}

func parseTestdataVersion(path string) int {
	path = filepath.ToSlash(strings.ToLower(path))
	path = strings.TrimPrefix(path, "testdata/postgres.v")
	path = strings.TrimSuffix(path, ".sql")

	v, err := strconv.Atoi(path)
	if err != nil {
		fmt.Fprintf(os.Stderr, "invalid testdata path %q\n", path)
		return -1
	}
	return v
}

var testMigrationFormat = `// AUTOGENERATED BY migrategen.go
// DO NOT EDIT.

package satellitedb

import "storj.io/storj/private/migrate"

// testMigration returns migration that can be used for testing.
func (db *satelliteDB) testMigration() *migrate.Migration {
	return &migrate.Migration{
		Table: "versions",
		Steps: []*migrate.Step{
			{
				DB:          &db.migrationDB,
				Description: "Testing setup",
				Version:     %d,
				Action:      migrate.SQL{` + "`%s`" + `},
			},
		},
	}
}
`