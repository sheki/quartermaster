package quartermaster

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"

	"github.com/golang/glog"
)

type DeployAgent struct {
	repoName      string
	repoPath      string
	incoming      chan DockerHubBuild
	stage2        chan DockerHubBuild
	outgoingReady chan struct{}
	slackWebhook  string
}

func NewStartedAgent(repoName, repoPath, slackWebhook string) *DeployAgent {
	a := &DeployAgent{
		repoPath:      repoPath,
		repoName:      repoName,
		incoming:      make(chan DockerHubBuild, 1000),
		stage2:        make(chan DockerHubBuild),
		outgoingReady: make(chan struct{}),
		slackWebhook:  slackWebhook,
	}
	go a.deploy()
	go a.promote()
	return a
}

func (d *DeployAgent) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	defer req.Body.Close()
	var build DockerHubBuild
	if err := json.NewDecoder(req.Body).Decode(&build); err != nil {
		writeErr(w, err)
		return
	}
	if d.repoName != build.Repository.RepoName {
		writeErr(w, fmt.Errorf("unkonw repo %s", build.Repository.RepoName))
		return
	}

	if build.PushData.Tag != "latest" {
		glog.Infoln("Non latest build, ignore")
		return
	}

	glog.Infoln("got a request enqueue a build", build)

	select {
	case d.incoming <- build:
	case <-time.After(20 * time.Second):
		writeErr(w, fmt.Errorf("timed out writing, build server busy"))
		return
	}
	d.postMsg(fmt.Sprintf("Docker image %s successfully built", build.Repository.RepoName), ":docker:")
	w.Write([]byte("ok"))
}

func (d *DeployAgent) postMsg(msg, emoji string) {
	payload := map[string]string{
		"channel":    "#software",
		"username":   "Quartermaster",
		"text":       msg,
		"icon_emoji": emoji,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		glog.Errorln(err)
		return
	}
	resp, err := http.PostForm(
		d.slackWebhook,
		url.Values{"payload": {string(b)}},
	)
	if err != nil {
		glog.Error(err)
	}
	resp.Body.Close()
}

func (d *DeployAgent) promote() {
	var workingSet []DockerHubBuild
	for {
		select {
		case i := <-d.incoming:
			workingSet = append(workingSet, i)
		case <-d.outgoingReady:
			if len(workingSet) == 0 {
				break
			}
			var winner DockerHubBuild
			for _, v := range workingSet {
				if v.PushData.PushedAt > winner.PushData.PushedAt {
					winner = v
				}
			}
			workingSet = make([]DockerHubBuild, 0)
			d.stage2 <- winner
		}
	}
}

func (d *DeployAgent) processSingleBuild(build DockerHubBuild) {
	defer func() {
		d.outgoingReady <- struct{}{}
	}()
	glog.Infoln("processing ", build)

	d.postMsg("Deploying "+build.Repository.RepoName, ":spock-hand:")

	if err := os.Chdir(d.repoPath); err != nil {
		glog.Errorln(err)
		return
	}
	if err := gitPull(); err != nil {
		glog.Errorln(err)
		return
	}
	msg, err := ebDeploy()
	glog.Infoln(msg)
	if err != nil {
		d.postMsg(
			fmt.Sprintf(
				"Deploy probably failed for %s with message %s",
				build.Repository.RepoName,
				msg,
			),
			":sparkling_heart:",
		)
		glog.Errorln(err)
		return
	}
	d.postMsg("Deploy success "+build.Repository.RepoName, ":sparkling_heart:")
	log.Println("eb deploy done")
}

func ebDeploy() (string, error) {
	b, err := exec.Command("eb", "deploy", "--timeout", "20").Output()
	return string(b), err
}

func gitPull() error {
	msg, err := exec.Command("git", "pull").Output()
	log.Println("GIT PULL OUTPUT", string(msg))
	return err
}

func (d *DeployAgent) deploy() {
	// always start with I am ready
	for {
		select {
		case b := <-d.stage2:
			d.processSingleBuild(b)
		case <-time.After(30 * time.Second):
			d.outgoingReady <- struct{}{}
		}
	}
}

func writeErr(w http.ResponseWriter, err error) {
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(err.Error()))
}
