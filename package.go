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

var pkgSubCommandTab = map[string]func(args []string){
	"build":    pkgBuildMain,
	"deploy":   pkgDeployMain,
	"list":     pkgListMain,
	"delete":   pkgDeleteMain,
	"describe": pkgDescribeMain,
	"help":     pkgHelpMain,
}

func pkgMain(args []string) {
	exitStatus := 0

	pkgSubCommandName := "help"
	if len(args) == 0 {
		exitStatus = 1
	} else {
		pkgSubCommandName = args[0]
	}

	pkgSubCommand, ok := pkgSubCommandTab[pkgSubCommandName]
	if !ok {
		exitStatus = 1
		pkgSubCommand = pkgHelpMain
	}

	if len(args) > 0 {
		args = args[1:]
	}

	pkgSubCommand(args)

	os.Exit(exitStatus)
}

func pkgBuildMain(args []string) {
	type buildOpts struct {
		common commonOpts
	}

	var opts buildOpts

	f := flag.NewFlagSet("bopmatic package build", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err := f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	proj, err := bopsdk.NewProject(opts.common.projectFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if proj.Desc.BuildCmd == "" {
		fmt.Printf("Project %v is a static site only; no build required\n",
			proj.Desc.Name)
		os.Exit(0)
	}

	err = proj.Build(os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to build %v: %v\n", proj.Desc.Name, err)
		os.Exit(1)
	}

	err = proj.RemoveStalePackages()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to remove stale packages: %v\n", err)
		os.Exit(1)
	}

	pkg, err := proj.NewPackageCreate("", os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to package %v: %v\n", proj.Desc.Name, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully built pkgId:%v (%v)\n", pkg.Id,
		pkg.AbsTarballPath())
	fmt.Printf("To deploy your package, next run:\n\t'bopmatic package deploy'\n")
}

func pkgDeployMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type deployOpts struct {
		common commonOpts
	}

	var opts deployOpts

	f := flag.NewFlagSet("bopmatic package deploy", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	proj, err := bopsdk.NewProject(opts.common.projectFilename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	pkg, err := proj.NewPackageExisting("")
	if err != nil {
		_ = proj.RemoveStalePackages()

		pkg, err = proj.NewPackageCreate("", os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to package %v: %v\n", proj.Desc.Name, err)
			os.Exit(1)
		}
	}

	validateNoConflicts(sdkOpts, pkg)

	fmt.Printf("Deploying pkgId:%v (%v)...", pkg.Id, pkg.AbsTarballPath())
	// @todo specify envId
	deployId, err := pkg.Deploy("", sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started\nDeploying takes about 10 minutes. You can check deploy progress with:\n\t'bopmatic deploy describe --deployid %v'\n",
		deployId)
}

func validateNoConflicts(sdkOpts []bopsdk.DeployOption, pkg *bopsdk.Package) {
	// @todo for UX purposes consider evaluating conflicts client-side here
	// rather than just relying on server-side conflict checks
}

func pkgListMain(args []string) {
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

	f := flag.NewFlagSet("bopmatic package list", flag.ExitOnError)
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

	if opts.common.projectId == "" {
		fmt.Printf("Listing packages for all projects...")
	} else {
		fmt.Printf("Listing packages for project %v...",
			opts.common.projectId)
	}

	pkgs, err := bopsdk.ListPackages(opts.common.projectId, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(pkgs) == 0 {
		fmt.Printf("\nNo currently deployed packages\n")
	} else {
		fmt.Printf("\nProjectId\t\t\tPackageId\n")

		for _, pkg := range pkgs {
			fmt.Printf("%v\t\t%v\n", pkg.ProjId, pkg.PackageId)
		}
	}
}

//go:embed pkgHelp.txt
var pkgHelpText string

func pkgHelpMain(args []string) {
	fmt.Printf(pkgHelpText)
}

func pkgDescribeMain(args []string) {
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

	f := flag.NewFlagSet("bopmatic package describe", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.packageId == "" {
		fmt.Fprintf(os.Stderr, "Please specify package id with --pkgid. If you don't know this, try 'bopmatic package list'\n")
		os.Exit(1)
	}

	fmt.Printf("Describing pkgId:%v...", opts.common.packageId)
	pkgDesc, err := bopsdk.Describe(opts.common.packageId, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nPackageId %v:\n\tProjectId: %v\n\tState: %v\n\tSize: %v MiB\n\tUploadTime: %v\n",
		pkgDesc.PackageId, pkgDesc.ProjId, pkgDesc.State,
		pkgDesc.PackageSize/1024/1024, unixTime2UtcStr(pkgDesc.UploadTime))

	switch pkgDesc.State {
	case pb.PackageState_UPLOADING:
		fmt.Printf("\nYour project is being uploaded to Bopmatic ServiceRunner\n")
	case pb.PackageState_UPLOADED:
		fmt.Printf("\nYour project package was uploaded Bopmatic ServiceRunner and will next be validated\n")
	case pb.PackageState_PKG_VALIDATING:
		fmt.Printf("\nBopmatic ServiceRunner is validating your project package\n")
	case pb.PackageState_INVALID:
		fmt.Printf("\nSomething is wrong with your project package and it cannot	be deployed. Please delete it with:\n\t'bopmatic package destroy --pkgid %v'\n",
			pkgDesc.PackageId)
	case pb.PackageState_PKG_BUILDING:
		fmt.Printf("\nBopmatic ServiceRunner is building infrastructure for your project package\n")
	case pb.PackageState_BUILT:
		fmt.Printf("\nBopmatic ServiceRunner has built your project.\n\n")
	case pb.PackageState_PKG_DELETED:
		fmt.Printf("\nBopmatic ServiceRunner has deleted your project package\n")
	case pb.PackageState_PKG_SUPPORT_NEEDED:
		fallthrough
	case pb.PackageState_UNKNOWN_PKG_STATE:
		fallthrough
	default:
		fmt.Printf("\nAn error occurred within Bopmatic ServiceRunner and a support staff member needs to examine the situation.\n")
	}
}

func pkgDeleteMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	type deleteOpts struct {
		common commonOpts
	}

	var opts deleteOpts

	f := flag.NewFlagSet("bopmatic package delete", flag.ExitOnError)
	setCommonFlags(f, &opts.common)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if opts.common.packageId == "" {
		fmt.Fprintf(os.Stderr, "Please specify package id with --pkgid. If you don't know this, try 'bopmatic package list'\n")
		os.Exit(1)
	}

	fmt.Printf("Listing packages...")
	pkgs, err := bopsdk.ListPackages(opts.common.projectId, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	found := false
	for _, pkg := range pkgs {
		if pkg.PackageId == opts.common.packageId {
			found = true
		}
	}

	if !found {
		fmt.Printf("\nPackage id %v no longer exists\n", opts.common.packageId)
		os.Exit(1)
	}

	fmt.Printf("Deleting pkgId:%v...", opts.common.packageId)
	err = bopsdk.DeletePackage(opts.common.packageId, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\nDeleted pkgId:%v", opts.common.packageId)
}
