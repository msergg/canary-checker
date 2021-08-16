package checks

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/flanksource/kommons"

	"github.com/flanksource/commons/logger"

	"github.com/flanksource/canary-checker/api/external"
	v1 "github.com/flanksource/canary-checker/api/v1"
	"github.com/flanksource/canary-checker/pkg"
	"github.com/flanksource/commons/text"
	"github.com/hirochachacha/go-smb2"
)

type SmbChecker struct {
	kommons *kommons.Client `yaml:"-" json:"-"`
}

func (c *SmbChecker) SetClient(client *kommons.Client) {
	c.kommons = client
}

func (c SmbChecker) GetClient() *kommons.Client {
	return c.kommons
}

type SmbStatus struct {
	age   string
	count int
}

func (c *SmbChecker) Type() string {
	return "smb"
}

func (c *SmbChecker) Run(canary v1.Canary) []*pkg.CheckResult {
	var results []*pkg.CheckResult
	for _, conf := range canary.Spec.Smb {
		results = append(results, c.Check(canary, conf))
	}
	return results
}

func (c *SmbChecker) Check(canary v1.Canary, extConfig external.Check) *pkg.CheckResult {
	start := time.Now()
	smbCheck := extConfig.(v1.SmbCheck)
	template := smbCheck.GetDisplayTemplate()
	port := smbCheck.GetPort()
	namespace := canary.Namespace
	var smbStatus SmbStatus
	var err error
	textResults := smbCheck.GetDisplayTemplate() != ""
	var serverPath string
	auth, err := GetAuthValues(smbCheck.Auth, c.kommons, namespace)
	if err != nil {
		return smbFailF(smbCheck, textResults, smbStatus, template, "failed getting auth details: %v", err)
	}
	if strings.Contains(smbCheck.Server, "\\") {
		serverPath, smbCheck.Sharename, smbCheck.SearchPath, err = getServerDetails(smbCheck.Server, port)
		if err != nil {
			return smbFailF(smbCheck, textResults, smbStatus, template, "error fetching server details: %v. serverPath: %v", err, serverPath)
		}
	} else {
		serverPath = fmt.Sprintf("%s:%d", smbCheck.Server, port)
	}
	if smbCheck.SearchPath == "" {
		smbCheck.SearchPath = "."
	}
	conn, err := net.Dial("tcp", serverPath)
	if err != nil {
		return smbFailF(smbCheck, textResults, smbStatus, template, "failed getting connection: %v", err)
	}
	defer conn.Close()
	d := &smb2.Dialer{
		Initiator: &smb2.NTLMInitiator{
			User:        auth.Username.Value,
			Password:    auth.Password.Value,
			Domain:      smbCheck.Domain,
			Workstation: smbCheck.Workstation,
		},
	}

	s, err := d.Dial(conn)
	if err != nil {
		return smbFailF(smbCheck, textResults, smbStatus, template, "failed connecting to server: %v", err)
	}
	defer s.Logoff() //nolint: errcheck
	fs, err := s.Mount(smbCheck.Sharename)
	if err != nil {
		return smbFailF(smbCheck, textResults, smbStatus, template, "failed mounting sharname %v to server: %v", smbCheck.Sharename, err)
	}
	defer fs.Umount() //nolint: errcheck
	age, count, err := getLatestFileAgeAndCount(fs, smbCheck.SearchPath)
	if err != nil {
		return smbFailF(smbCheck, textResults, smbStatus, template, "error traversing files: %v", err)
	}
	smbStatus.age = text.HumanizeDuration(age)
	smbStatus.count = count
	if smbCheck.MinAge != "" {
		minAge, err := time.ParseDuration(smbCheck.MinAge)
		if err != nil {
			return smbFailF(smbCheck, textResults, smbStatus, template, "error parsing minAge: %v", err)
		}
		if age < minAge {
			return smbFailF(smbCheck, textResults, smbStatus, template, "age of latest object %v is less than the minimum age: %v ", age, minAge)
		}
	}
	if smbCheck.MaxAge != "" {
		maxAge, err := time.ParseDuration(smbCheck.MaxAge)
		if err != nil {
			return smbFailF(smbCheck, textResults, smbStatus, template, "error parsing minAge: %v", err)
		}
		if age > maxAge {
			return smbFailF(smbCheck, textResults, smbStatus, template, "age of latest object %v is more than the maximum age: %v ", age, maxAge)
		}
	}
	if count < smbCheck.MinCount {
		return smbFailF(smbCheck, textResults, smbStatus, template, "file count: %v is less than specified minCount: %v", count, smbCheck.MinCount)
	}
	var results = map[string]interface{}{"age": smbStatus.age, "count": smbStatus.count}
	message, err := text.TemplateWithDelims(template, "[[", "]]", results)
	if err != nil {
		return smbFailF(smbCheck, textResults, smbStatus, template, "error templating the message: %v", err)
	}
	return Successf(smbCheck, start, textResults, message)
}

func getServerDetails(serverPath string, port int) (server, sharename, searchPath string, err error) {
	serverPath = strings.TrimLeft(serverPath, "\\")
	if serverPath == "" {
		return "", "", "", fmt.Errorf("error parsing server path")
	}
	serverDetails := strings.SplitN(serverPath, "\\", 3)
	server = fmt.Sprintf("%s:%d", serverDetails[0], port)
	switch len(serverDetails) {
	case 1:
		return "", "", "", fmt.Errorf("error parsing server path")
	case 2:
		logger.Tracef("searchPath not found in the server path. Default '.' will be taken")
		sharename = serverDetails[1]
		searchPath = "."
		return
	default:
		sharename = serverDetails[1]
		searchPath = strings.ReplaceAll(serverDetails[2], "\\", "/")
		return
	}
}

func getLatestFileAgeAndCount(fs *smb2.Share, searchPath string) (duration time.Duration, count int, err error) {
	files, err := fs.ReadDir(searchPath)
	if err != nil {
		return
	}
	if len(files) == 0 {
		// directory is empty. returning duration of directory
		info, err := fs.Stat(searchPath)
		if err != nil {
			return duration, count, err
		}
		return time.Since(info.ModTime()), 0, nil
	}
	duration = time.Since(files[0].ModTime())
	for _, file := range files {
		if file.IsDir() {
			continue
		}
		if duration >= time.Since(file.ModTime()) {
			duration = time.Since(file.ModTime())
		}
		count++
	}
	return
}

func smbFailF(check external.Check, textResults bool, smbStatus SmbStatus, template, msg string, args ...interface{}) *pkg.CheckResult {
	var results = map[string]interface{}{"age": smbStatus.age, "count": smbStatus.count}
	message := smbTemplateResult(template, results)
	message = message + "\n" + fmt.Sprintf(msg, args...)
	return TextFailf(check, textResults, message)
}

func smbTemplateResult(template string, results map[string]interface{}) (message string) {
	message, err := text.TemplateWithDelims(template, "[[", "]]", results)
	if err != nil {
		message = message + "\n" + err.Error()
	}
	return message
}
