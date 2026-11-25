package pm

import (
	"bytes"
	"crypto/x509"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"software.sslmate.com/src/go-pkcs12"

	"gitlab.com/gitlab-org/cli/internal/dbg"
)

const (
	trustStoreProperty         = "javax.net.ssl.trustStore"
	trustStorePasswordProperty = "javax.net.ssl.trustStorePassword"
	trustStoreTypeProperty     = "javax.net.ssl.trustStoreType"
	jdkCACertsPassword         = "changeit"
)

// jksMagic and jceksMagic open the proprietary keystore formats go-pkcs12
// cannot read. The JVM sniffs the format rather than trusting
// trustStoreType, so the store is checked the same way.
var (
	jksMagic   = []byte{0xfe, 0xed, 0xfe, 0xed}
	jceksMagic = []byte{0xce, 0xce, 0xce, 0xce}
)

var trustStoreProperties = []string{trustStoreProperty, trustStorePasswordProperty, trustStoreTypeProperty}

// jvmTrustProps returns the truststore properties a JVM started with the -D
// options in sources has, over those in defaults. Later sources override
// earlier ones, as later options do on the java command line.
func jvmTrustProps(defaults map[string]string, sources ...string) map[string]string {
	props := make(map[string]string, len(trustStoreProperties))
	for _, name := range trustStoreProperties {
		if value, ok := defaults[name]; ok {
			props[name] = value
		}
	}
	for _, src := range sources {
		for _, arg := range splitJVMArgs(src) {
			rest, ok := strings.CutPrefix(arg, "-D")
			if !ok {
				continue
			}
			name, value, _ := strings.Cut(rest, "=")
			switch name {
			case trustStoreProperty, trustStorePasswordProperty, trustStoreTypeProperty:
				props[name] = value
			}
		}
	}
	return props
}

// jvmTrustAnchors returns the certificates in the truststore a JVM with the
// truststore properties props would trust. Relative paths resolve against
// dir, the JVM's working directory. Like JSSE, it reads the JDK default under
// JAVA_HOME when no trustStore is set, and cacerts when the named store is not
// a readable regular file. It returns an error for a named store it reads but
// cannot decode. A JDK default it cannot decode is logged only under
// GLAB_DEBUG, because JDK 17 and earlier ship it as JKS.
func jvmTrustAnchors(env []string, dir string, props map[string]string) ([]*x509.Certificate, error) {
	path, password, storeType := props[trustStoreProperty], props[trustStorePasswordProperty], props[trustStoreTypeProperty]
	if t := strings.ToLower(storeType); strings.HasPrefix(t, "windows-") || strings.HasPrefix(t, "keychainstore") {
		// The Windows and macOS stores back the system roots that registry
		// connections already start from.
		dbg.Debugf("dependency firewall: JVM truststore type %s is the system store, which registry connections already trust", storeType)
		return nil, nil
	}

	javaHome := lookupEnv(env, "JAVA_HOME")
	_, named := props[trustStoreProperty]
	switch {
	case path == "NONE":
		return nil, nil
	case !named:
		path = jdkDefaultTrustStore(javaHome)
	default:
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		raw, err := readRegularFile(path)
		if err != nil {
			// The path stays out of this line: a misparsed option can carry
			// the next one, such as the password.
			dbg.Debugf("dependency firewall: the JVM trustStore option names no readable regular file (%v), so reading cacerts as the JVM does", err)
			path = jdkCACertsPath(javaHome)
			break
		}
		certs, err := decodeJVMTrustStore(raw, storeType, password)
		if err != nil {
			return nil, fmt.Errorf("registry connections do not trust JVM truststore %q: %w", path, err)
		}
		return certs, nil
	}
	if path == "" {
		return nil, nil
	}

	if _, set := props[trustStorePasswordProperty]; !set {
		password = jdkCACertsPassword
	}
	raw, err := os.ReadFile(path)
	var certs []*x509.Certificate
	if err == nil {
		certs, err = decodeJVMTrustStore(raw, storeType, password)
	}
	if err != nil {
		dbg.Debugf("dependency firewall: skipping JVM truststore %q: %v", path, err)
		return nil, nil
	}
	return certs, nil
}

// readRegularFile reads path if it is a readable regular file, which JSSE
// requires of a truststore; reading a FIFO would block. Its errors leave the
// path out, so they are safe to log for a misparsed option.
func readRegularFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err == nil && !info.Mode().IsRegular() {
		return nil, errors.New("not a regular file")
	}
	var raw []byte
	if err == nil {
		raw, err = os.ReadFile(path)
	}
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return nil, pathErr.Err
	}
	return raw, err
}

// jvmTrustStoreOverride reports a truststore option in one of laterVars, which
// reach the JVM after optsVar and so replace the truststore glab gives the
// child there; tool then distrusts the proxy.
func jvmTrustStoreOverride(env []string, tool, optsVar string, laterVars ...string) error {
	for _, v := range laterVars {
		for _, arg := range splitJVMArgs(lookupEnv(env, v)) {
			rest, ok := strings.CutPrefix(arg, "-D")
			if !ok {
				continue
			}
			if name, value, _ := strings.Cut(rest, "="); replacesTrustStore(name, value) {
				return fmt.Errorf("-D%s in %s replaces glab's truststore, so %s cannot trust the dependency firewall proxy; set it in %s instead", name, v, tool, optsVar)
			}
		}
	}
	return nil
}

// replacesTrustStore reports whether -Dname=value replaces the truststore glab
// gives the child when it takes effect after glab's options. A PKCS12 or JKS
// trustStoreType is harmless: under the default keystore.type.compat both
// load glab's PKCS#12.
func replacesTrustStore(name, value string) bool {
	switch name {
	case trustStoreProperty, trustStorePasswordProperty:
		return true
	case trustStoreTypeProperty:
		return !strings.EqualFold(value, "pkcs12") && !strings.EqualFold(value, "jks")
	}
	return false
}

// jdkDefaultTrustStore returns the store JSSE loads when no trustStore is
// set: jssecacerts when present, otherwise cacerts.
func jdkDefaultTrustStore(javaHome string) string {
	if javaHome == "" {
		return ""
	}
	if jsse := filepath.Join(javaHome, "lib", "security", "jssecacerts"); isFile(jsse) {
		return jsse
	}
	return jdkCACertsPath(javaHome)
}

func jdkCACertsPath(javaHome string) string {
	if javaHome == "" {
		return ""
	}
	return filepath.Join(javaHome, "lib", "security", "cacerts")
}

func decodeJVMTrustStore(raw []byte, storeType, password string) ([]*x509.Certificate, error) {
	switch strings.ToLower(storeType) {
	case "", "pkcs12", "jks", "jceks":
	default:
		return nil, fmt.Errorf("type %s is not supported", storeType)
	}
	if bytes.HasPrefix(raw, jksMagic) || bytes.HasPrefix(raw, jceksMagic) {
		return nil, errors.New("JKS and JCEKS keystores are not supported, convert it to PKCS#12 with keytool -importkeystore")
	}
	certs, err := pkcs12.DecodeTrustStore(raw, password)
	if err == nil {
		return certs, nil
	}
	if password != "" {
		// The JDK's own cacerts (JDK 18 and later) has no MAC, which
		// go-pkcs12 accepts only with an empty password; the JVM ignores the
		// password for such a store.
		if certs, retryErr := pkcs12.DecodeTrustStore(raw, ""); retryErr == nil {
			return certs, nil
		}
	}
	switch {
	case !errors.Is(err, pkcs12.ErrIncorrectPassword):
		return nil, fmt.Errorf("%w; glab reads PKCS#12 truststores that hold only trusted certificates", err)
	case password == "":
		// Without a password the JVM skips the MAC check; go-pkcs12 cannot.
		return nil, fmt.Errorf("%w; set -D%s so glab can check the store", err, trustStorePasswordProperty)
	}
	return nil, err
}

// splitJVMArgs splits s into arguments one line at a time, on whitespace
// outside single or double quotes, and drops the quotes. Backslashes stay
// literal so Windows paths survive. A line with an unbalanced quote is split
// on whitespace with its quotes kept, so that quote cannot run on into the
// next option or line. Quotes follow the JVM, Gradle and Maven 4; Maven 3's
// Unix launcher passes them to java literally.
func splitJVMArgs(s string) []string {
	var args []string
	for line := range strings.Lines(s) {
		lineArgs, balanced := splitQuotedArgs(line)
		if !balanced {
			lineArgs = strings.Fields(line)
		}
		args = append(args, lineArgs...)
	}
	return args
}

func splitQuotedArgs(line string) ([]string, bool) {
	var (
		args  []string
		cur   strings.Builder
		inArg bool
		quote rune
	)
	for _, r := range line {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote, inArg = r, true
		case unicode.IsSpace(r):
			if inArg {
				args = append(args, cur.String())
				cur.Reset()
				inArg = false
			}
		default:
			cur.WriteRune(r)
			inArg = true
		}
	}
	if inArg {
		args = append(args, cur.String())
	}
	return args, quote == 0
}

// mavenJVMConfig returns the contents of the .mvn/jvm.config that the mvn
// launcher adds to MAVEN_OPTS, or "" when there is none. Like Maven 4's
// launcher (Maven 3 substitutes nothing), it strips comments before it
// substitutes MAVEN_PROJECTBASEDIR, so a '#' in the project path is not read
// as a comment.
func mavenJVMConfig(env []string, dir string, args []string) string {
	base := mavenBaseDir(env, dir, args)

	raw, ok, err := readConfigFile(filepath.Join(base, ".mvn", "jvm.config"))
	if err != nil {
		dbg.Debugf("dependency firewall: %v; not reading .mvn/jvm.config for a JVM truststore", err)
	}
	if !ok {
		return ""
	}
	return strings.NewReplacer("${MAVEN_PROJECTBASEDIR}", base, "$MAVEN_PROJECTBASEDIR", base).Replace(stripJVMConfigComments(string(raw)))
}

// mavenArgProperties returns the -D properties mvn's own arguments set:
// .mvn/maven.config, then MAVEN_ARGS, then the command line, the later
// winning as it does for mvn. MAVEN_OPTS is left out, for the caller to read
// among the JVM options.
func mavenArgProperties(env []string, dir string, args []string) map[string]string {
	cli := mavenConfigArgs(mavenBaseDir(env, dir, args))
	cli = append(cli, strings.Fields(lookupEnv(env, "MAVEN_ARGS"))...)
	return mavenProperties(nil, append(cli, args...))
}

// argTrustStoreOverride reports, with format and the option's name, a
// truststore option in props, the system properties a tool's own arguments
// set. Maven 3 and gradle apply those over the JVM options, and so over the
// truststore glab gives them there.
func argTrustStoreOverride(props map[string]string, format string) error {
	for _, name := range trustStoreProperties {
		if value, ok := props[name]; ok && replacesTrustStore(name, value) {
			return fmt.Errorf(format, name)
		}
	}
	return nil
}

// stripJVMConfigComments cuts each jvm.config line at a '#' that starts a
// word. Maven 4 cuts at any '#'; Maven 3 strips nothing, and a word starting
// with '#' makes it fail (java takes the word as its main class), so this is
// what both agree on. A '#' inside a word is kept, as Maven 3 keeps it.
func stripJVMConfigComments(s string) string {
	var b strings.Builder
	for line := range strings.Lines(strings.NewReplacer("\r\n", "\n", "\r", "\n").Replace(s)) {
		prev := ' '
		for i, r := range line {
			if r == '#' && unicode.IsSpace(prev) {
				line = line[:i] + "\n"
				break
			}
			prev = r
		}
		b.WriteString(line)
	}
	return b.String()
}
