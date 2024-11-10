/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"flag"
	"fmt"
	"os"

	_ "embed"

	bopsdk "github.com/bopmatic/sdk/golang"
	"github.com/bopmatic/sdk/golang/pb"
)

var deploySubCommandTab = map[string]func(args []string){
	"list":     deployListMain,
	"describe": deployDescribeMain,
	"help":     deployHelpMain,
}

//go:embed deployHelp.txt
var deployHelpText string

func deployHelpMain(args []string) {
	fmt.Printf(deployHelpText)
}

func deployListMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type listOpts struct {
		common commonOpts
	}

	var opts listOpts

	f := flag.NewFlagSet("bopmatic deploy list", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.projectId == "" {
		proj, err := bopsdk.NewProject(opts.common.projectFilename)
		if err == nil {
			opts.common.projectId = proj.Desc.Id
		}
	}

	fmt.Printf("Listing deployments for projId:%v...", opts.common.projectId)

	// @todo add envId
	deployments, err := bopsdk.ListDeployments(opts.common.projectId, "",
		sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(deployments) == 0 {
		fmt.Printf("\nNo currently deployed packages\n")
	} else {
		fmt.Printf("\nDeployment Id\n")

		for _, deployId := range deployments {
			fmt.Printf("%v\n", deployId)
		}
	}
}

func deployMain(args []string) {
	exitStatus := 0

	deploySubCommandName := "help"
	if len(args) == 0 {
		exitStatus = 1
	} else {
		deploySubCommandName = args[0]
	}

	deploySubCommand, ok := deploySubCommandTab[deploySubCommandName]
	if !ok {
		exitStatus = 1
		deploySubCommand = deployHelpMain
	}

	if len(args) > 0 {
		args = args[1:]
	}

	deploySubCommand(args)

	os.Exit(exitStatus)
}

func deployDescribeMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type describeOpts struct {
		common commonOpts
	}

	var opts describeOpts

	f := flag.NewFlagSet("bopmatic deploy describe", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.deployId == "" {
		fmt.Fprintf(os.Stderr, "Please specify deployment id with --deployid. If you don't know this, try 'bopmatic deployment list'\n")
		os.Exit(1)
	}

	fmt.Printf("Describing deployId:%v...", opts.common.deployId)
	deployDesc, err := bopsdk.DescribeDeployment(opts.common.deployId,
		sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nDeployment Id:%v\n\tProject Id:%v\n\tPackage Id:%v\n\tEnvironment Id:%v\n\tType:%v\n\tInitiator:%v\n\tState:%v\n\tDetail:%v\n\tCreate Time:%v\n\tValidation Start Time:%v\n\tBuild Start Time:%v\n\tDeploy Start Time:%v\n\tCompletion Time:%v\n",
		deployDesc.Id, deployDesc.Header.ProjId, deployDesc.Header.PkgId,
		deployDesc.Header.EnvId, deployDesc.Header.Type,
		deployDesc.Header.Initiator, deployDesc.State, deployDesc.StateDetail,
		deployDesc.CreateTime, deployDesc.ValidationStartTime,
		deployDesc.BuildStartTime, deployDesc.DeployStartTime,
		deployDesc.EndTime)

	switch deployDesc.State {
	case pb.DeploymentState_CREATED:
		fmt.Printf("\nYour deployment has been created and will be validated shortly\n")
	case pb.DeploymentState_DPLY_VALIDATING:
		fmt.Printf("\nBopmatic ServiceRunner is validating your project package\n")
	case pb.DeploymentState_DPLY_BUILDING:
		fmt.Printf("\nBopmatic ServiceRunner is building infrastructure for your project package\n")
	case pb.DeploymentState_DEPLOYING:
		fmt.Printf("\nBopmatic ServiceRunner is deploying your package into production\n")
	case pb.DeploymentState_SUCCESS:
		fmt.Printf("\nBopmatic ServiceRunner has successully completed this	deployment of your package\n")
	case pb.DeploymentState_FAILED:
		fallthrough
	case pb.DeploymentState_UNKNOWN_DEPLOY_STATE:
		fmt.Printf("\nAn error occurred within Bopmatic ServiceRunner and a support staff member needs to examine the situation.\n")
	}
}
