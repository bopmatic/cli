/* Copyright © 2022-2024 Bopmatic, LLC. All Rights Reserved.
 *
 * See LICENSE file at the root of this package for license terms
 */
package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"io/ioutil"
	"os"
	"os/user"
	"path"
	"path/filepath"
	"sort"
	"strings"

	_ "embed"

	bopsdk "github.com/bopmatic/sdk/golang"
	"github.com/bopmatic/sdk/golang/pb"
	"github.com/bopmatic/sdk/golang/util"
	"golang.org/x/sync/errgroup"
)

type projOpts struct {
	projectFilename string
	projectId       string
}

var projSubCommandTab = map[string]func(args []string){
	"create":     projCreateMain,
	"destroy":    projDestroyMain,
	"deactivate": projDeactivateMain,
	"list":       projListMain,
	"help":       projHelpMain,
	"describe":   projDescribeMain,
}

func projDescribeMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	var opts projOpts
	f := flag.NewFlagSet("bopmatic project describe", flag.ExitOnError)
	setProjFlags(f, &opts)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	err = setProjIdFromOpts(&opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	projDesc, err := bopsdk.DescribeProject(opts.projectId, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to describe project: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Project %v:\n", projDesc.Id)
	fmt.Printf("\tName: %v\n", projDesc.Header.Name)
	fmt.Printf("\tDnsPrefix: %v\n", projDesc.Header.DnsPrefix)
	fmt.Printf("\tDnsDomain: %v\n", projDesc.Header.DnsDomain)
	fmt.Printf("\tCreated: %v (%v)\n", unixTime2Utc(projDesc.CreateTime),
		unixTime2Local(projDesc.CreateTime))
	fmt.Printf("\tState: %v\n", projDesc.State)
	fmt.Printf("\tActive deployments: %v\n", projDesc.ActiveDeployIds)
	fmt.Printf("\tPending deployments: %v\n", projDesc.PendingDeployIds)

	if len(projDesc.ActiveDeployIds) == 0 {
		return
	}

	var wg errgroup.Group
	var descSiteReply *pb.DescribeSiteReply
	var svcDescList []*pb.DescribeServiceReply
	var dbDescList []*pb.DescribeDatabaseReply
	var dstoreDescList []*pb.DescribeDatastoreReply

	wg.Go(func() error {
		var err error
		descSiteReply, err = bopsdk.DescribeSite(projDesc.Id, "", sdkOpts...)
		return err
	})
	wg.Go(func() error {
		var err error
		svcDescList, err = bopsdk.DescribeAllServices(projDesc.Id, "",
			sdkOpts...)
		return err
	})
	wg.Go(func() error {
		var err error
		dbDescList, err = bopsdk.DescribeAllDatabases(projDesc.Id, "",
			sdkOpts...)
		return err
	})
	wg.Go(func() error {
		var err error
		dstoreDescList, err = bopsdk.DescribeAllDatastores(projDesc.Id, "",
			sdkOpts...)
		return err
	})

	err = wg.Wait()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to retrieve additional project details: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("\tWebsite: %v\n", descSiteReply.SiteEndpoint)

	for _, svcDesc := range svcDescList {
		fmt.Printf("\tService %v:\n", svcDesc.Desc.SvcHeader.ServiceName)
		fmt.Printf("\t\tApi Definition: %v\n", svcDesc.Desc.ApiDef)
		fmt.Printf("\t\tPort: %v\n", svcDesc.Desc.Port)
		if len(svcDesc.Desc.DatabaseNames) > 0 {
			fmt.Printf("\t\tDatabases: ")
			for _, dbName := range svcDesc.Desc.DatabaseNames {
				fmt.Printf("%v, ", dbName)
			}
			fmt.Printf("\n")
		}
		if len(svcDesc.Desc.DatastoreNames) > 0 {
			fmt.Printf("\t\tDatastores: ")
			for _, dstoreName := range svcDesc.Desc.DatastoreNames {
				fmt.Printf("%v, ", dstoreName)
			}
			fmt.Printf("\n")
		}
		if len(svcDesc.Desc.RpcEndpoints) > 0 {
			fmt.Printf("\t\tRpc Endpoints:\n")
			for _, rpcEnd := range svcDesc.Desc.RpcEndpoints {
				fmt.Printf("\t\t\t%v\n", rpcEnd)
			}
		}
	}

	for _, dbDesc := range dbDescList {
		fmt.Printf("\tDatabase %v:\n", dbDesc.Desc.DatabaseHeader.DatabaseName)
		if len(dbDesc.Desc.ServiceNames) > 0 {
			fmt.Printf("\t\tServices: ")
			for _, svcName := range dbDesc.Desc.ServiceNames {
				fmt.Printf("%v, ", svcName)
			}
			fmt.Printf("\n")
		}
		if len(dbDesc.Desc.Tables) > 0 {
			for _, tbl := range dbDesc.Desc.Tables {
				fmt.Printf("\t\tTable %v:\n", tbl.Name)
				fmt.Printf("\t\t\tNumRows: %v\n", tbl.NumRows)
				fmt.Printf("\t\t\tSize: %v MiB\n", tbl.Size/1024/1024)
			}
		}
	}

	for _, dstoreDesc := range dstoreDescList {
		fmt.Printf("\tDatastore %v:\n",
			dstoreDesc.Desc.DatastoreHeader.DatastoreName)
		fmt.Printf("\t\tNumObjects: %v\n", dstoreDesc.Desc.NumObjects)
		fmt.Printf("\t\tSize: %v MiB\n",
			dstoreDesc.Desc.CapacityConsumedInBytes/1024/1024)
		if len(dstoreDesc.Desc.ServiceNames) > 0 {
			fmt.Printf("\t\tServices: ")
			for _, svcName := range dstoreDesc.Desc.ServiceNames {
				fmt.Printf("%v, ", svcName)
			}
			fmt.Printf("\n")
		}
	}
}

func setProjIdFromOpts(opts *projOpts) error {
	if opts.projectId == "" {
		proj, err := bopsdk.NewProject(opts.projectFilename)
		if err != nil {
			err = fmt.Errorf("Could not find project file '%v': %v. Please specify --projid, --projfile, run from within a Bopmatic project directory.\n",
				opts.projectFilename, err)
			return err
		}
		opts.projectId = proj.Desc.Id
	}

	return nil
}

type ProjTemplate struct {
	name    string
	srcPath string
}

func selectProjectTemplateKey(tmplNameIn string,
	templateMap map[string]ProjTemplate) string {

	if tmplNameIn == "" {
		return ""
	}

	if _, ok := templateMap[tmplNameIn]; ok {
		return tmplNameIn
	}

	fmt.Fprintf(os.Stderr, "%v is not a valid project template\n", tmplNameIn)
	return ""
}

func readContainerDir(dir string) (dirEntries []string, err error) {

	ctx := context.Background()
	tmpBuf := new(bytes.Buffer)

	err = util.RunContainerCommand(ctx, []string{"ls", dir}, tmpBuf, os.Stderr)
	if err != nil {
		return nil, err
	}

	dirEntries = make([]string, 0)
	for _, entry := range strings.Split(tmpBuf.String(), "\n") {
		if entry != "" {
			dirEntries = append(dirEntries, entry)
		}
	}

	return dirEntries, nil
}

func fetchTemplateSet(subdirs []string) map[string]ProjTemplate {

	tmplSet := make(map[string]ProjTemplate)

	for _, subdir := range subdirs {
		dir := fmt.Sprintf("%v/%v", ExamplesDir, subdir)
		dirEntries, err := readContainerDir(dir)
		if err != nil {
			// can occur if user has an older build container'
			fmt.Fprintf(os.Stderr, "Failed to retrieve list of %v templates: %v. Skipping.\n",
				subdir, err)
			continue
		}

		for _, tmpl := range dirEntries {
			nameKey := subdir + "/" + tmpl
			tmplSet[nameKey] = ProjTemplate{
				name:    nameKey,
				srcPath: ExamplesDir + "/" + subdir + "/" + tmpl,
			}
		}
	}

	return tmplSet
}

func fetchTemplates() (serviceTemplates, clientTemplates map[string]ProjTemplate) {

	supportedLanguages := []string{"golang", "java", "python"}

	serviceTemplates = fetchTemplateSet(supportedLanguages)

	serviceTemplates["staticsite"] = ProjTemplate{
		name:    "staticsite",
		srcPath: ExamplesDir + "/staticsite",
	}

	clientTemplates = fetchTemplateSet([]string{ClientTemplateSubdir})

	return serviceTemplates, clientTemplates
}

func getUserInputsForNewPkg(serviceTemplates map[string]ProjTemplate) (
	selectedTmplKey, projectName string) {

	user, err := user.Current()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Unable to determine your username: %v", err)
		os.Exit(1)
	}

	fmt.Printf("Available project templates:\n")
	var templateKeysSorted []string
	for key, _ := range serviceTemplates {
		templateKeysSorted = append(templateKeysSorted, key)
	}

	sort.Strings(templateKeysSorted)
	for _, key := range templateKeysSorted {
		fmt.Printf("\t%v\n", key)
	}

	var templateName string
	for selectedTmplKey = ""; selectedTmplKey == ""; selectedTmplKey = selectProjectTemplateKey(templateName, serviceTemplates) {

		const defaultTemplateName = DefaultTemplate
		templateName = defaultTemplateName

		fmt.Printf("Enter Bopmatic Project Template [%v]: ", defaultTemplateName)
		fmt.Scanf("%s", &templateName)
		templateName = strings.TrimSpace(templateName)
	}

	for {
		projectName = user.Username + path.Base(templateName)
		fmt.Printf("Enter Bopmatic Project Name [%v]: ", projectName)
		fmt.Scanf("%s", &projectName)
		projectName = strings.TrimSpace(projectName)
		isGoodName, reason := bopsdk.IsGoodProjectName(projectName)
		if isGoodName {
			break
		} else {
			fmt.Fprintf(os.Stderr, "%v\n", reason)
		}
	}

	return selectedTmplKey, projectName
}

func replaceTemplateKeywordInFile(filename, existingText, replaceText string,
	ignoreIfNotExist bool) {

	fileContentBytes, err := ioutil.ReadFile(filename)
	if err != nil {
		if ignoreIfNotExist && os.IsNotExist(err) {
			return
		}
		fmt.Fprintf(os.Stderr, "Failed to set replace %v with %v in %v: %v",
			existingText, replaceText, filename, err)
		os.Exit(1)
	}
	fileContent := string(fileContentBytes)

	fileContent = strings.ReplaceAll(fileContent,
		strings.ToLower(existingText), strings.ToLower(replaceText))

	hasUpperCase := (strings.ToLower(existingText) != existingText)
	if hasUpperCase {
		fileContent = strings.ReplaceAll(fileContent, existingText, replaceText)
	}

	err = ioutil.WriteFile(filename, []byte(fileContent), 0644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to update %v: %v", filename, err)
		os.Exit(1)
	}
}

func createProjectFromTemplate(serviceTemplates, clientTemplates map[string]ProjTemplate,
	selectedTmplKey, projectName string) (projectDir, projectFile string) {

	ctx := context.Background()

	// copy project from template
	err := util.RunContainerCommand(ctx, []string{"cp", "-r",
		serviceTemplates[selectedTmplKey].srcPath, "./" + projectName},
		os.Stdout, os.Stderr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create project %v: %v", projectName,
			err)
		os.Exit(1)
	}

	// if there's a matching client template, replace site_assets with it
	tmplBase := path.Base(selectedTmplKey)
	clientTmplKey := ClientTemplateSubdir + "/" + tmplBase
	clientTmpl, ok := clientTemplates[clientTmplKey]
	if ok {
		siteAssetsDir := "./" + projectName + "/" + SiteAssetsSubdir
		err := util.RunContainerCommand(ctx, []string{"rm", "-rf",
			siteAssetsDir}, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to remove %v: %v", siteAssetsDir, err)
			os.Exit(1)
		}

		clientDir := "./" + projectName + "/" + ClientTemplateSubdir
		err = util.RunContainerCommand(ctx, []string{"cp", "-r",
			clientTmpl.srcPath, clientDir}, os.Stdout, os.Stderr)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to copy client assets into %v: %v",
				siteAssetsDir, err)
			os.Exit(1)
		}
	}

	// set the created project's name
	// @todo find a cleaner way to replace the project name
	projectDir = filepath.Join(".", projectName)
	projectFile = filepath.Join(projectDir, "Bopmatic.yaml")
	projectMakefile := filepath.Join(projectDir, "Makefile")
	clientMakefile := filepath.Join(projectDir, ClientTemplateSubdir, "Makefile")
	templateToken := filepath.Join(projectDir, "template_replace_keyword")

	templateKeyword, err := ioutil.ReadFile(templateToken)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to set project name %v: %v", projectName,
			err)
		os.Exit(1)
	}

	replaceTemplateKeywordInFile(projectFile, string(templateKeyword),
		projectName, false)
	replaceTemplateKeywordInFile(projectMakefile, string(templateKeyword),
		projectName, true)
	if ok {
		replaceTemplateKeywordInFile(clientMakefile, string(templateKeyword),
			projectName, true)
	}

	_ = os.Remove(templateToken)

	return projectDir, projectFile
}

func projCreateMain(args []string) {
	// @todo get project id via sr's CreateProject() primitive
	haveBuildImg, err := util.HasBopmaticBuildImage()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	if !haveBuildImg {
		fmt.Fprintf(os.Stderr, "Could not find Bopmatic Build Image; please run:\n\n\tbopmatic config\n")
		os.Exit(1)
	}

	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	serviceTemplates, clientTemplates := fetchTemplates()

	selectedTmplKey, projectName := getUserInputsForNewPkg(serviceTemplates)

	projectDir, projectFile := createProjectFromTemplate(serviceTemplates,
		clientTemplates, selectedTmplKey, projectName)

	// validate everything worked
	proj, err := bopsdk.NewProject(projectFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Created project %v but it fails to parse: %v",
			projectDir, err)
		os.Exit(1)
	}

	err = proj.Register(sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Created project %v but it failed to register: %v",
			projectDir, err)
		os.Exit(1)
	}

	fmt.Printf("Successfully created .%v%v:\n%v", string(os.PathSeparator),
		projectDir, proj.String())

	fmt.Printf("\nTo build your new project next run:\n\t'cd %v; bopmatic package build'\n",
		projectDir)
}

func projDestroyMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	var opts projOpts
	f := flag.NewFlagSet("bopmatic project describe", flag.ExitOnError)
	setProjFlags(f, &opts)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	err = setProjIdFromOpts(&opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Destroying projectId:%v...", opts.projectId)
	err = bopsdk.UnregisterProject(opts.projectId, sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to destroy project: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("done.\nProject %v was successfully deleted\n",
		opts.projectId)
}

func projDeactivateMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	var opts projOpts
	f := flag.NewFlagSet("bopmatic project deactivate", flag.ExitOnError)
	setProjFlags(f, &opts)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	err = setProjIdFromOpts(&opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// @todo implement environment ids
	fmt.Printf("Deactivating projId:%v...", opts.projectId)
	deployId, err := bopsdk.DeactivateProject(opts.projectId, "", sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to deactivate project: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Started\nDeactivationg takes about 10 minutes. You can check progress with:\n\t'bopmatic deploy describe --deployid %v'\n",
		deployId)
}

//go:embed projHelp.txt
var projHelpText string

func projHelpMain(args []string) {
	fmt.Printf(projHelpText)
}

func setProjFlags(f *flag.FlagSet, o *projOpts) {
	f.StringVar(&o.projectFilename, "projfile", bopsdk.DefaultProjectFilename,
		"Bopmatic project filename")
	f.StringVar(&o.projectId, "projid", "", "Bopmatic project id")
}

func projListMain(args []string) {
	sdkOpts, err := getAuthSdkOpts()
	if err != nil {
		fmt.Fprintf(os.Stderr,
			"Failed to get user creds; did you run bompatic config? err: %v\n",
			err)
		os.Exit(1)
	}

	f := flag.NewFlagSet("bopmatic project list", flag.ExitOnError)

	err = f.Parse(args)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	// @todo add envId
	projects, err := bopsdk.ListProjects(sdkOpts...)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	if len(projects) == 0 {
		fmt.Printf("\nNo projects exist; create a new one with 'bopmatic project create'\n")
	} else {
		fmt.Printf("Project Id\n")
		fmt.Printf("-----------------------\n")

		for _, projId := range projects {
			fmt.Printf("%v\n", projId)
		}
	}
}

func projMain(args []string) {
	exitStatus := 0

	projSubCommandName := "help"
	if len(args) == 0 {
		exitStatus = 1
	} else {
		projSubCommandName = args[0]
	}

	projSubCommand, ok := projSubCommandTab[projSubCommandName]
	if !ok {
		exitStatus = 1
		projSubCommand = projHelpMain
	}

	if len(args) > 0 {
		args = args[1:]
	}

	projSubCommand(args)

	os.Exit(exitStatus)
}
