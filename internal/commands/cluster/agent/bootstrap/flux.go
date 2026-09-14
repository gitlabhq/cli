package bootstrap

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path"
	"time"

	"github.com/avast/retry-go/v5"
	"go.yaml.in/yaml/v3"
)

var _ FluxWrapper = (*localFluxWrapper)(nil)

func NewLocalFluxWrapper(
	cmd Cmd,
	binary string,
	manifestPath string,
	helmRepositoryAddress string,
	helmRepositoryName string,
	helmRepositoryNamespace string,
	helmRepositoryFilepath string,
	helmReleaseName string,
	helmReleaseNamespace string,
	helmReleaseFilepath string,
	helmReleaseTargetNamespace string,
	helmReleaseValues []string,
	helmReleaseValuesFrom []string,
	fluxSourceType string,
	fluxSourceNamespace string,
	fluxSourceName string,
) FluxWrapper {
	return &localFluxWrapper{
		cmd:                        cmd,
		binary:                     binary,
		manifestPath:               manifestPath,
		helmRepositoryAddress:      helmRepositoryAddress,
		helmRepositoryName:         helmRepositoryName,
		helmRepositoryNamespace:    helmRepositoryNamespace,
		helmRepositoryFilepath:     helmRepositoryFilepath,
		helmReleaseName:            helmReleaseName,
		helmReleaseNamespace:       helmReleaseNamespace,
		helmReleaseFilepath:        helmReleaseFilepath,
		helmReleaseTargetNamespace: helmReleaseTargetNamespace,
		helmReleaseValues:          helmReleaseValues,
		helmReleaseValuesFrom:      helmReleaseValuesFrom,
		fluxSourceType:             fluxSourceType,
		fluxSourceNamespace:        fluxSourceNamespace,
		fluxSourceName:             fluxSourceName,

		reconcileRetryDelay: 10 * time.Second,
	}
}

type localFluxWrapper struct {
	cmd                        Cmd
	binary                     string
	manifestPath               string
	helmRepositoryAddress      string
	helmRepositoryName         string
	helmRepositoryNamespace    string
	helmRepositoryFilepath     string
	helmReleaseName            string
	helmReleaseNamespace       string
	helmReleaseFilepath        string
	helmReleaseTargetNamespace string
	helmReleaseValues          []string
	helmReleaseValuesFrom      []string
	fluxSourceType             string
	fluxSourceNamespace        string
	fluxSourceName             string
	reconcileRetryDelay        time.Duration
}

func (f *localFluxWrapper) createHelmRepositoryManifest() (file, error) {
	helmRepositoryYAML, err := f.cmd.RunWithOutput(
		f.binary,
		"create",
		"source",
		"helm",
		f.helmRepositoryName,
		"--export",
		fmt.Sprintf("-n=%s", f.helmRepositoryNamespace),
		fmt.Sprintf("--url=%s", f.helmRepositoryAddress),
	)
	if err != nil {
		return file{}, err
	}

	return file{path: path.Join(f.manifestPath, f.helmRepositoryFilepath), content: helmRepositoryYAML}, nil
}

type agentHelmChartValues struct {
	Config *agentHelmChartValuesConfig `yaml:"config"`
}

type agentHelmChartValuesConfig struct {
	KASAddress string `yaml:"kasAddress"`
	SecretName string `yaml:"secretName"`
}

func (f *localFluxWrapper) createHelmReleaseManifest(kasAddress string) (file, error) {
	// create temporary file for Flux CLI to read values from.
	// The Flux CLI does not yet support reading values from literal flags.
	valuesFile, err := os.CreateTemp("", "glab-bootstrap-helmrelease-values")
	if err != nil {
		return file{}, err
	}
	defer os.Remove(valuesFile.Name())
	defer valuesFile.Close()

	cfg := &agentHelmChartValues{
		Config: &agentHelmChartValuesConfig{
			KASAddress: kasAddress,
			SecretName: "gitlab-agent-token",
		},
	}

	enc := yaml.NewEncoder(valuesFile)
	if err := enc.Encode(cfg); err != nil {
		return file{}, err
	}

	args := []string{
		"create",
		"helmrelease",
		f.helmReleaseName,
		"--export",
		fmt.Sprintf("-n=%s", f.helmReleaseNamespace),
		fmt.Sprintf("--target-namespace=%s", f.helmReleaseTargetNamespace),
		"--create-target-namespace=true",
		fmt.Sprintf("--source=HelmRepository/%s.%s", f.helmRepositoryName, f.helmRepositoryNamespace),
		"--chart=gitlab-agent",
		fmt.Sprintf("--release-name=%s", f.helmReleaseName),
		fmt.Sprintf("--values=%s", valuesFile.Name()),
	}

	for _, v := range f.helmReleaseValues {
		args = append(args, fmt.Sprintf("--values=%s", v))
	}
	for _, v := range f.helmReleaseValuesFrom {
		args = append(args, fmt.Sprintf("--values-from=%s", v))
	}

	helmReleaseYAML, err := f.cmd.RunWithOutput(
		f.binary,
		args...,
	)
	if err != nil {
		return file{}, err
	}

	return file{path: path.Join(f.manifestPath, f.helmReleaseFilepath), content: helmReleaseYAML}, nil
}

func (f *localFluxWrapper) reconcile() error {
	// reconcile flux source to pull new HelmRepository source
	err := f.cmd.Run(f.binary, "reconcile", "source", f.fluxSourceType, f.fluxSourceName, fmt.Sprintf("-n=%s", f.fluxSourceNamespace))
	if err != nil {
		return err
	}

	// just reconciling doesn't mean that the HelmRelease now exists ... (bug in flux? At least very unfortunate behavior)
	err = retry.New(retry.Attempts(6), retry.Delay(f.reconcileRetryDelay)).Do(func() error {
		output, err := f.cmd.RunWithOutput(f.binary, "get", "helmreleases", f.helmReleaseName, fmt.Sprintf("-n=%s", f.helmReleaseNamespace))
		if err != nil {
			// flux always returns with exit code 0, even when the helmrelease does not exist (yet)
			return retry.Unrecoverable(err)
		}

		if bytes.Contains(output, fmt.Appendf(nil, `HelmRelease object '%s' not found in "%s" namespace`, f.helmReleaseName, f.helmReleaseNamespace)) {
			return errors.New(string(output))
		}

		return nil
	})
	if err != nil {
		return err
	}

	return f.cmd.Run(f.binary, "reconcile", "helmrelease", f.helmReleaseName, fmt.Sprintf("-n=%s", f.helmReleaseNamespace), "--with-source")
}
