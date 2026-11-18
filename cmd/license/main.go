// Command license 提供离线许可的签发、校验与吊销工具。
//
// 用法：
//
//	license keygen -dir .
//	license issue -key private.pem -id L-001 -product goframe -customer ACME \
//	  -edition enterprise -features sso,audit -limits users=100,devices=50 \
//	  -expires 2027-12-31T23:59:59Z -grace 168h -machines fp-1 -out license.json
//	license inspect -public public.pem -license license.json
//	license revoke -key private.pem -ids L-001,L-002 -out revocation.json
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/god-jason/goframe/license"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run 执行命令行并把输出写入 stdout/stderr，返回进程退出码。
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) < 1 {
		usage(stderr)
		return 2
	}
	switch args[0] {
	case "keygen":
		return runKeygen(args[1:], stdout, stderr)
	case "issue":
		return runIssue(args[1:], stdout, stderr)
	case "inspect":
		return runInspect(args[1:], stdout, stderr)
	case "revoke":
		return runRevoke(args[1:], stdout, stderr)
	case "fingerprint":
		return runFingerprint(args[1:], stdout, stderr)
	case "-h", "--help", "help":
		usage(stderr)
		return 0
	default:
		fmt.Fprintf(stderr, "unknown command %q\n\n", args[0])
		usage(stderr)
		return 2
	}
}

func usage(w io.Writer) {
	fmt.Fprint(w, `Usage:
  license keygen  -dir . [-private private.pem] [-public public.pem]
  license issue   -key private.pem -id ID -product NAME -out license.json [options]
  license inspect -public public.pem -license license.json [-fingerprint FP] [-revocation FILE]
  license revoke  -key private.pem -ids ID1,ID2 -out revocation.json
  license fingerprint [-sources uuid,hostid,disk,cpuid,mac]
`)
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(stderr)
	return flags
}

// parseFlags 把 flag 解析错误转换为退出码。
func parseFlags(flags *flag.FlagSet, args []string, stderr io.Writer) int {
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		usage(stderr)
		return 2
	}
	return -1
}

func runKeygen(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("keygen", stderr)
	dir := flags.String("dir", ".", "output directory")
	privatePath := flags.String("private", "private.pem", "private key file name")
	publicPath := flags.String("public", "public.pem", "public key file name")
	if code := parseFlags(flags, args, stderr); code >= 0 {
		return code
	}

	if err := os.MkdirAll(*dir, 0o700); err != nil {
		return fail(stderr, "create output directory", err)
	}
	publicPEM, privatePEM, err := license.GenerateKeyPair()
	if err != nil {
		return fail(stderr, "generate key pair", err)
	}
	privateFile := filepath.Join(*dir, *privatePath)
	publicFile := filepath.Join(*dir, *publicPath)
	if err := os.WriteFile(privateFile, privatePEM, 0o600); err != nil {
		return fail(stderr, "write private key", err)
	}
	if err := os.WriteFile(publicFile, publicPEM, 0o644); err != nil {
		return fail(stderr, "write public key", err)
	}
	fmt.Fprintf(stdout, "private key: %s\npublic key:  %s\n", privateFile, publicFile)
	if info, err := license.CollectFingerprint(context.Background(), license.FingerprintOptions{}); err == nil {
		fmt.Fprintf(stdout, "machine fingerprint: %s (source: %s)\n", info.Fingerprint, info.Source)
	}
	return 0
}

func runFingerprint(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("fingerprint", stderr)
	sources := flags.String("sources", "", "comma separated priority: uuid,hostid,disk,cpuid,mac")
	if code := parseFlags(flags, args, stderr); code >= 0 {
		return code
	}

	info, err := license.CollectFingerprint(context.Background(), license.FingerprintOptions{
		Sources: splitList(*sources),
	})
	if err != nil {
		return fail(stderr, "collect fingerprint", err)
	}
	fmt.Fprintf(stdout, "fingerprint: %s\nsource: %s\n", info.Fingerprint, info.Source)
	for _, name := range []string{
		license.SourceUUID,
		license.SourceHostID,
		license.SourceDisk,
		license.SourceCPUID,
		license.SourceMAC,
	} {
		if value := info.Components[name]; value != "" {
			fmt.Fprintf(stdout, "%-7s %s\n", name+":", value)
		}
	}
	return 0
}

func runIssue(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("issue", stderr)
	keyPath := flags.String("key", "", "private key PEM file")
	out := flags.String("out", "license.json", "output license file")
	id := flags.String("id", "", "license id")
	product := flags.String("product", "", "product name")
	customer := flags.String("customer", "", "customer name")
	edition := flags.String("edition", "enterprise", "edition")
	features := flags.String("features", "", "comma separated features")
	limits := flags.String("limits", "", "comma separated key=value limits, -1 means unlimited")
	machines := flags.String("machines", "", "comma separated machine fingerprints")
	metadata := flags.String("metadata", "", "comma separated key=value metadata")
	expires := flags.String("expires", "", "expiration time in RFC3339")
	notBefore := flags.String("not-before", "", "not-before time in RFC3339")
	issuedAt := flags.String("issued-at", "", "issue time in RFC3339, defaults to now")
	grace := flags.Duration("grace", 0, "grace period after expiration, e.g. 168h")
	if code := parseFlags(flags, args, stderr); code >= 0 {
		return code
	}

	if *keyPath == "" || *id == "" || *product == "" {
		fmt.Fprintln(stderr, "-key, -id and -product are required")
		return 2
	}
	keyPEM, err := os.ReadFile(*keyPath)
	if err != nil {
		return fail(stderr, "read private key", err)
	}
	privateKey, err := license.ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return fail(stderr, "parse private key", err)
	}

	parsedLimits, code := parseLimits(*limits, stderr)
	if code != 0 {
		return code
	}
	options := license.IssueOptions{
		ID:          *id,
		Product:     *product,
		Customer:    *customer,
		Edition:     *edition,
		GracePeriod: *grace,
		Features:    splitList(*features),
		Machines:    splitList(*machines),
		Limits:      parsedLimits,
		Metadata:    parseMetadata(*metadata),
	}
	if *issuedAt != "" {
		value, err := time.Parse(time.RFC3339, *issuedAt)
		if err != nil {
			return fail(stderr, "parse issued-at", err)
		}
		options.IssuedAt = value
	}
	if *expires != "" {
		value, err := time.Parse(time.RFC3339, *expires)
		if err != nil {
			return fail(stderr, "parse expires", err)
		}
		options.ExpiresAt = &value
	}
	if *notBefore != "" {
		value, err := time.Parse(time.RFC3339, *notBefore)
		if err != nil {
			return fail(stderr, "parse not-before", err)
		}
		options.NotBefore = &value
	}

	content, issued, err := license.Issue(options, privateKey)
	if err != nil {
		return fail(stderr, "issue license", err)
	}
	if err := os.WriteFile(*out, content, 0o600); err != nil {
		return fail(stderr, "write license", err)
	}
	fmt.Fprintf(stdout, "license written: %s\nid: %s\nproduct: %s\n", *out, issued.ID, issued.Product)
	if issued.ExpiresAt != nil {
		fmt.Fprintf(stdout, "expires: %s\n", issued.ExpiresAt.Format(time.RFC3339))
	}
	return 0
}

func runInspect(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("inspect", stderr)
	publicPath := flags.String("public", "public.pem", "public key PEM file")
	licensePath := flags.String("license", "license.json", "license file")
	fingerprint := flags.String("fingerprint", "", "override machine fingerprint")
	revocationPath := flags.String("revocation", "", "optional revocation list file")
	if code := parseFlags(flags, args, stderr); code >= 0 {
		return code
	}

	publicPEM, err := os.ReadFile(*publicPath)
	if err != nil {
		return fail(stderr, "read public key", err)
	}
	publicKey, err := license.ParsePublicKeyPEM(publicPEM)
	if err != nil {
		return fail(stderr, "parse public key", err)
	}
	content, err := os.ReadFile(*licensePath)
	if err != nil {
		return fail(stderr, "read license", err)
	}
	lic, err := license.VerifyLicense(content, publicKey)
	if err != nil {
		return fail(stderr, "verify license", err)
	}

	var revocation *license.RevocationList
	if *revocationPath != "" {
		content, err := os.ReadFile(*revocationPath)
		if err != nil {
			return fail(stderr, "read revocation list", err)
		}
		revocation, err = license.VerifyRevocation(content, publicKey)
		if err != nil {
			return fail(stderr, "verify revocation list", err)
		}
	}

	machine := *fingerprint
	if machine == "" {
		machine, _ = license.Fingerprint(context.Background())
	}
	status, reason := license.Evaluate(lic, time.Now(), machine, revocation)
	fmt.Fprintf(stdout, "status: %s\nvalid: %t\n", status, status.Valid())
	if reason != "" {
		fmt.Fprintf(stdout, "reason: %s\n", reason)
	}
	if machine != "" {
		fmt.Fprintf(stdout, "fingerprint: %s\n", machine)
	}
	if !status.Valid() {
		return 1
	}
	return 0
}

func runRevoke(args []string, stdout, stderr io.Writer) int {
	flags := newFlagSet("revoke", stderr)
	keyPath := flags.String("key", "private.pem", "private key PEM file")
	ids := flags.String("ids", "", "comma separated license ids")
	out := flags.String("out", "revocation.json", "output revocation file")
	reasons := flags.String("reasons", "", "comma separated id=reason")
	if code := parseFlags(flags, args, stderr); code >= 0 {
		return code
	}

	if *ids == "" {
		fmt.Fprintln(stderr, "-ids is required")
		return 2
	}
	keyPEM, err := os.ReadFile(*keyPath)
	if err != nil {
		return fail(stderr, "read private key", err)
	}
	privateKey, err := license.ParsePrivateKeyPEM(keyPEM)
	if err != nil {
		return fail(stderr, "parse private key", err)
	}
	list := &license.RevocationList{
		IDs:     splitList(*ids),
		Reasons: parseMetadata(*reasons),
	}
	content, err := license.SignRevocation(list, privateKey)
	if err != nil {
		return fail(stderr, "sign revocation list", err)
	}
	if err := os.WriteFile(*out, content, 0o600); err != nil {
		return fail(stderr, "write revocation list", err)
	}
	fmt.Fprintf(stdout, "revocation written: %s\nrevoked: %d\n", *out, len(list.IDs))
	return 0
}

func splitList(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		item = strings.TrimSpace(item)
		if item != "" {
			result = append(result, item)
		}
	}
	return result
}

func parseLimits(value string, stderr io.Writer) (map[string]int64, int) {
	limits := make(map[string]int64)
	for _, item := range splitList(value) {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			fmt.Fprintln(stderr, "invalid limit: "+item)
			return nil, 2
		}
		number, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
		if err != nil {
			fmt.Fprintf(stderr, "parse limit %s: %v\n", item, err)
			return nil, 1
		}
		limits[strings.TrimSpace(parts[0])] = number
	}
	return limits, 0
}

func parseMetadata(value string) map[string]string {
	metadata := make(map[string]string)
	for _, item := range splitList(value) {
		parts := strings.SplitN(item, "=", 2)
		if len(parts) != 2 {
			continue
		}
		metadata[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
	}
	return metadata
}

func fail(stderr io.Writer, action string, err error) int {
	fmt.Fprintf(stderr, "%s: %v\n", action, err)
	return 1
}
