package commands

import (
	"context"
	"fmt"
	"github.com/coreos/go-semver/semver"
	"github.com/jfrog/frogbot/commands/utils"
	"github.com/jfrog/froggit-go/vcsclient"
	xrayutils "github.com/jfrog/jfrog-cli-core/v2/xray/utils"
	clientLog "github.com/jfrog/jfrog-client-go/utils/log"
	"github.com/jfrog/jfrog-client-go/xray/services"
	clitool "github.com/urfave/cli/v2"
	"os/exec"
	"strings"
)

func CreateFixPullRequests(c *clitool.Context) error {
	// Get params and VCS client
	params, client, err := utils.GetParamsAndClient()
	if err != nil {
		return err
	}
	// Send usage report
	usageReportSent := make(chan error)
	go utils.ReportUsage(c.Command.Name, &params.Server, usageReportSent)

	// Do scan commit
	scanResults, err := scanCommit(params)
	if err != nil {
		return err
	}

	// Fix and create PRs
	err = fixImpactedPackagesAndCreatePRs(params, client, scanResults)
	if err != nil {
		return err
	}

	// Wait for usage report
	<-usageReportSent
	return err
}

// Audit the dependencies of the current commit.
func scanCommit(params *utils.FrogbotParams) ([]services.ScanResponse, error) {
	// Audit commit code
	xrayScanParams := createXrayScanParams(params.Watches, params.Project)
	scanResults, err := auditSource(xrayScanParams, params)
	if err != nil {
		return nil, err
	}
	clientLog.Info("Xray scan completed")
	return scanResults, nil
}

// Audit the dependencies of the current branch.
func fixImpactedPackagesAndCreatePRs(params *utils.FrogbotParams, client vcsclient.VcsClient, scanResults []services.ScanResponse) error {
	fixVersionsMap, err := createFixVersionsMap(scanResults)
	if err != nil {
		return err
	}
	clientLog.Info("Found", len(fixVersionsMap), "impacted packages with fix versions")

	gitManager, err := utils.NewGitManager(".", "origin")
	if err != nil {
		return err
	}
	for impactedPackage, fixVersionInfo := range fixVersionsMap {
		clientLog.Info("Fixing", impactedPackage, "with", fixVersionInfo.fixVersion)
		err = fixSinglePackageAndCreatePR(impactedPackage, *fixVersionInfo, params, client, gitManager)
		if err != nil {
			clientLog.Error("failed while trying to fix and create PR for:", impactedPackage, "with version:", fixVersionInfo.fixVersion, "with error:", err.Error())
		}
	}
	return nil
}

// Create vulnerabilities rows. The rows should contain only the new issues added by this PR
func createFixVersionsMap(scanResults []services.ScanResponse) (map[string]*FixVersionInfo, error) {
	fixVersionsMap := map[string]*FixVersionInfo{}
	for _, scanResult := range scanResults {
		if len(scanResult.Violations) > 0 {
			// todo!
		} else if len(scanResult.Vulnerabilities) > 0 {
			vulnerabilities, err := xrayutils.PrepareVulnerabilities(scanResult.Vulnerabilities, false, false)
			if err != nil {
				return nil, err
			}
			for _, vulnerability := range vulnerabilities {
				if vulnerability.FixedVersions != nil && len(vulnerability.FixedVersions) > 0 {
					fixVersion := parseVersionChangeString(vulnerability.FixedVersions[0])
					fixVersionInfo, exists := fixVersionsMap[vulnerability.ImpactedPackageName]
					if exists {
						fixVersionInfo.UpdateFixVersion(fixVersion)
					} else {
						fixVersionsMap[vulnerability.ImpactedPackageName] = NewFixVersionInfo(fixVersion, vulnerability.ImpactedPackageType)
					}
				}
			}
		}
	}
	return fixVersionsMap, nil
}

func fixSinglePackageAndCreatePR(impactedPackage string, fixVersionInfo FixVersionInfo, params *utils.FrogbotParams, client vcsclient.VcsClient, gitManager *utils.GitManager) (err error) {
	fixBranchName := fmt.Sprintf("%s-%s-%s-%s", "frogbot", fixVersionInfo.packageType, impactedPackage, fixVersionInfo.fixVersion)
	exists, err := gitManager.BranchExistsOnRemote(fixBranchName)
	if err != nil {
		return err
	}
	if exists {
		clientLog.Info("Branch:", fixBranchName, "already exists on remote.")
		return
	}
	clientLog.Info("Creating branch:", fixBranchName)
	err = gitManager.CreateBranch(fixBranchName)
	if err != nil {
		return err
	}
	clientLog.Info("Running git checkout to:", fixBranchName)
	err = gitManager.Checkout(fixBranchName)
	if err != nil {
		return err
	}
	defer func() {
		e := gitManager.Checkout(params.BaseBranch)
		if err == nil {
			err = e
		}
	}()

	switch fixVersionInfo.packageType {
	// todo: get package types from core
	// todo: place each type on different file
	case "Go":
		fixedImpactPackage := impactedPackage + "@v" + fixVersionInfo.fixVersion
		clientLog.Info(fmt.Sprintf("Running 'go get %s'", fixedImpactPackage))
		var output []byte
		output, err = exec.Command("go", "get", fixedImpactPackage).CombinedOutput() // #nosec G204
		if err != nil {
			err = fmt.Errorf("go get command failed: %s - %s", err.Error(), output)
			return
		}
	default:
	}

	clientLog.Info(fmt.Sprintf("Running git add all & commit: %s.", fixBranchName))
	commitString := fmt.Sprintf("[frogbot] Upgrade %s to %s", impactedPackage, fixVersionInfo.fixVersion)
	err = gitManager.AddAll()
	if err != nil {
		return err
	}
	err = gitManager.Commit(commitString)
	if err != nil {
		return err
	}
	clientLog.Info("Pushing fix branch:", fixBranchName)
	err = gitManager.Push(params.Token)
	if err != nil {
		return err
	}
	clientLog.Info("Creating Pull Request for:", fixBranchName)
	err = client.CreatePullRequest(context.Background(), params.RepoOwner, params.Repo, fixBranchName, params.BaseBranch, commitString, "PR body")
	return
}

///      1.0         --> 1.0 ≤ x
///      (,1.0]      --> x ≤ 1.0
///      (,1.0)      --> x &lt; 1.0
///      [1.0]       --> x == 1.0
///      (1.0,)      --> 1.0 &lt; x
///      (1.0, 2.0)   --> 1.0 &lt; x &lt; 2.0
///      [1.0, 2.0]   --> 1.0 ≤ x ≤ 2.0
func parseVersionChangeString(fixVersion string) string {
	latestVersion := strings.Split(fixVersion, ",")[0]
	if latestVersion[0] == '(' {
		return ""
	}
	latestVersion = strings.Trim(latestVersion, "[")
	latestVersion = strings.Trim(latestVersion, "]")
	return latestVersion
}

type FixVersionInfo struct {
	fixVersion  string
	packageType string
}

func NewFixVersionInfo(newFixVersion, packageType string) *FixVersionInfo {
	return &FixVersionInfo{newFixVersion, packageType}
}

func (fvi *FixVersionInfo) UpdateFixVersion(newFixVersion string) {
	// todo: change to NewVersion with error handling
	if fvi.fixVersion == "" || semver.New(newFixVersion).LessThan(*semver.New(fvi.fixVersion)) {
		fvi.fixVersion = newFixVersion
	}
}
