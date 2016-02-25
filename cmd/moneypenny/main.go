package main

import (
	"flag"
	"net/http"

	"github.com/sheki/moneypenny"
)

func main() {
	var repoName = flag.String("reponame", "", "the name of the repo like brightsolar/api")
	var repoPath = flag.String("repopath", "", "filesystem path where the repo is cheked out")
	var slackWebhook = flag.String("slackwebhook", "", "slack webhook url")
	var addr = flag.String("addr", ":8080", "http addr for the server")
	flag.Parse()
	agent := moneypenny.NewStartedAgent(*repoName, *repoPath, *slackWebhook)

	http.Handle("/deploy", agent)
	http.ListenAndServe(*addr, nil)
}
