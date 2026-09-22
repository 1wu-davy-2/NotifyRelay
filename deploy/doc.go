// Package deploy holds the deployment artefacts and the checks that keep them
// honest.
//
// It contains no code that ships. It exists so that the Dockerfile, the compose
// file, the Helm chart and the systemd unit sit next to a test that reads them:
// none of those can be built or installed on a development machine, and an
// artefact nobody can run is an artefact nobody notices is broken.
//
// The checks here are deliberately shallow — that a file parses, that a
// template compiles, that a directive is present. They cannot prove the image
// builds or the chart installs; only Docker and Helm can, and where those are
// unavailable the honest thing is a check that catches typos and a note saying
// what is still unverified.
package deploy
