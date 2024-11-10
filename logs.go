/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"os"
	"time"

	_ "embed"

	"github.com/araddon/dateparse"

	bopsdk "github.com/bopmatic/sdk/golang"
)

//go:embed logsHelp.txt
var logsHelpText string

func logsMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type logsOpts struct {
		common commonOpts
	}

	var opts logsOpts

	f := flag.NewFlagSet("bopmatic logs", flag.ExitOnError)
	setCommonFlags(f, &opts.common)
	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		fmt.Fprintf(os.Stderr, "%v\n", logsHelpText)
		os.Exit(1)
	}

	projId := opts.common.projectId
	var proj *bopsdk.Project
	if projId == "" {
		proj, err = bopsdk.NewProject(opts.common.projectFilename)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				fmt.Fprintf(os.Stderr, "Please specify --projid or run from within a Bopmatic project directory.\n")
				fmt.Fprintf(os.Stderr, "%v\n", logsHelpText)
			} else {
				fmt.Fprintf(os.Stderr, "%v\n", err)
			}
			os.Exit(1)
		}
		projId = proj.Desc.Id
	}
	svcName := opts.common.serviceName
	if svcName == "" {
		if proj != nil {
			if len(proj.Desc.Services) == 1 {
				svcName = proj.Desc.Services[0].Name
			} else {
				svcList := make([]string, 0)
				for _, svc := range proj.Desc.Services {
					svcList = append(svcList, svc.Name)
				}

				fmt.Fprintf(os.Stderr, "Please specify --svcname. Project %v currently has %v services: %v\n",
					projId, len(svcList), svcList)
				os.Exit(1)
			}
		} else {
			fmt.Fprintf(os.Stderr, "Please specify --svcname.")
			os.Exit(1)
		}
	}

	var startTime, endTime time.Time
	const DefaultLogWindow = 48 * time.Hour
	if opts.common.startTime == "" {
		startTime = time.Now().UTC().Add(-DefaultLogWindow)
	} else {
		startTime, err = dateparse.ParseAny(opts.common.startTime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not parse start time(%v): %v\n",
				opts.common.startTime, err)
			os.Exit(1)
		}
	}
	if opts.common.endTime == "" {
		endTime = time.Now().UTC()
	} else {
		endTime, err = dateparse.ParseAny(opts.common.endTime)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Could not parse end time(%v): %v\n",
				opts.common.endTime, err)
			os.Exit(1)
		}
	}
	if !endTime.After(startTime) {
		fmt.Fprintf(os.Stderr, "End time(%v) <= start time(%v). Please specify an end time that occurs later than start time.\n",
			endTime, startTime)
		os.Exit(1)
	}

	// @todo specify environment id
	err = bopsdk.GetLogs(projId, "", svcName, startTime, endTime, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
}
