package session

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gitlab.com/gitlab-org/cli/internal/dependencyfirewall/pm"
)

// JVMTrustEnv returns the environment that routes Maven and Gradle through the
// proxy and makes their JVMs trust its CA, writing the settings.xml and PKCS#12
// trust stores they need into stateDir. JVMs ignore the system trust store and
// (for Maven) the HTTP(S)_PROXY variables that cover the other managers, so they
// need a generated settings.xml and JVM trust store instead.
//
// Maven and Gradle each write their trust store to "<ca>.p12", so each is given
// a distinct CA copy under stateDir to avoid clobbering the other's store (the
// generated store passwords differ). Each trust store also folds in the system
// roots, so public dependencies the proxy does not intercept still validate.
//
// It returns whatever environment it could build together with an error
// naming each ecosystem it could not cover, so the caller can report a
// partial setup rather than leave mvn or gradle silently bypassing the proxy.
func JVMTrustEnv(stateDir, proxyURL, proxyCAPath string, warnf func(string, ...any)) ([]string, error) {
	caPEM, err := os.ReadFile(proxyCAPath)
	if err != nil {
		return nil, err
	}
	var env []string
	var errs []error

	// Maven: MAVEN_ARGS carries a -gs settings file with the proxy entries,
	// MAVEN_OPTS carries the trust store.
	mavenCA := filepath.Join(stateDir, "maven-ca.pem")
	if err := os.WriteFile(mavenCA, caPEM, 0o644); err != nil {
		errs = append(errs, fmt.Errorf("maven: %w", err))
	} else {
		args, argsErr := pm.MavenJobArgsEnv(mavenCA+".settings.xml", proxyURL, warnf)
		opts := pm.JVMTrustOpts(mavenCA)
		switch {
		case argsErr != nil:
			errs = append(errs, fmt.Errorf("maven: %w", argsErr))
		case opts == "":
			errs = append(errs, errors.New("maven: could not write the JVM trust store"))
		default:
			env = append(env, args, "MAVEN_OPTS="+opts)
		}
	}

	// Gradle: GRADLE_OPTS carries both routing (-Dhttp(s).proxy*) and the trust
	// store, as for a per-command run. It is not mirrored into JAVA_TOOL_OPTIONS,
	// which would proxy every JVM in the job and ignore NO_PROXY.
	gradleCA := filepath.Join(stateDir, "gradle-ca.pem")
	if err := os.WriteFile(gradleCA, caPEM, 0o644); err != nil {
		errs = append(errs, fmt.Errorf("gradle: %w", err))
	} else {
		gradle := pm.Gradle()
		gradle.Environment(proxyURL)
		gradleEnv := gradle.CATrustEnviron(gradleCA)
		if len(gradleEnv) == 0 {
			errs = append(errs, errors.New("gradle: no GRADLE_OPTS was produced"))
		}
		env = append(env, gradleEnv...)
	}
	return env, errors.Join(errs...)
}
