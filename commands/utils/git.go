package utils

import (
	"fmt"
	"github.com/jfrog/jfrog-client-go/utils"
)

type gitManager struct {
	manager utils.GitManager
}

func NewGitManager(dotGitPath string) *gitManager {
	return &gitManager{manager: *utils.NewGitManager(dotGitPath)}
}

func parseGitError(output string, errString string, err error) error {
	if err != nil {
		return fmt.Errorf("failed Running Git command: %s. %s - %s", err.Error(), output, errString)
	}
	return nil
}

func (gm *gitManager) CreateBranch(branch string) error {
	return parseGitError(gm.manager.ExecGit("branch", branch))
}

func (gm *gitManager) Checkout(branch string) error {
	return parseGitError(gm.manager.ExecGit("checkout", branch))
}

func (gm *gitManager) CreateBranchAndCheckout(branch string) error {
	err := gm.CreateBranch(branch)
	if err != nil {
		return err
	}
	return gm.Checkout(branch)
}

func (gm *gitManager) Add(fileName string) error {
	return parseGitError(gm.manager.ExecGit("add", fileName))
}

func (gm *gitManager) AddAll() error {
	return parseGitError(gm.manager.ExecGit("add", "-A"))
}

func (gm *gitManager) Commit(commitMessage string) error {
	return parseGitError(gm.manager.ExecGit("commit", "-m", commitMessage))
}

func (gm *gitManager) Config(key, value string) error {
	return parseGitError(gm.manager.ExecGit("config", key, value))
}

func (gm *gitManager) AddAllAndCommit(commitMessage string) error {
	err := gm.AddAll()
	if err != nil {
		return err
	}
	return gm.Commit(commitMessage)
}

func (gm *gitManager) Push(token, branch string) error {
	err := gm.manager.Push("sverdlov93", token)
	if err != nil {
		return err
	}
	return parseGitError(gm.manager.ExecGit("push", "https://"+token+"@github.com/sverdlov93/frogbot.git", branch))
}
