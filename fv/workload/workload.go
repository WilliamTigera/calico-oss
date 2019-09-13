// Copyright (c) 2017-2019 Tigera, Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package workload

import (
	"bufio"
	"fmt"
	"io"
	"os/exec"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/types"
	log "github.com/sirupsen/logrus"

	"github.com/projectcalico/libcalico-go/lib/backend/k8s/conversion"
	"github.com/projectcalico/libcalico-go/lib/options"

	"github.com/projectcalico/libcalico-go/lib/set"

	"github.com/projectcalico/felix/fv/containers"
	"github.com/projectcalico/felix/fv/infrastructure"
	"github.com/projectcalico/felix/fv/utils"
	api "github.com/projectcalico/libcalico-go/lib/apis/v3"
	client "github.com/projectcalico/libcalico-go/lib/clientv3"
)

type Workload struct {
	C                *containers.Container
	Name             string
	InterfaceName    string
	IP               string
	Ports            string
	DefaultPort      string
	runCmd           *exec.Cmd
	outPipe          io.ReadCloser
	errPipe          io.ReadCloser
	namespacePath    string
	WorkloadEndpoint *api.WorkloadEndpoint
	Protocol         string // "tcp" or "udp"
}

var workloadIdx = 0
var sideServIdx = 0
var permConnIdx = 0

func (w *Workload) Stop() {
	if w == nil {
		log.Info("Stop no-op because nil workload")
	} else {
		log.WithField("workload", w).Info("Stop")
		outputBytes, err := utils.Command("docker", "exec", w.C.Name,
			"cat", fmt.Sprintf("/tmp/%v", w.Name)).CombinedOutput()
		Expect(err).NotTo(HaveOccurred())
		pid := strings.TrimSpace(string(outputBytes))
		err = utils.Command("docker", "exec", w.C.Name, "kill", pid).Run()
		Expect(err).NotTo(HaveOccurred())
		_, err = w.runCmd.Process.Wait()
		if err != nil {
			log.WithField("workload", w).Error("failed to wait for process")
		}
		log.WithField("workload", w).Info("Workload now stopped")
	}
}

func Run(c *infrastructure.Felix, name, profile, ip, ports, protocol string) (w *Workload) {
	w, err := run(c, name, profile, ip, ports, protocol)
	if err != nil {
		log.WithError(err).Info("Starting workload failed, retrying")
		w, err = run(c, name, profile, ip, ports, protocol)
	}
	Expect(err).NotTo(HaveOccurred())

	return w
}

func run(c *infrastructure.Felix, name, profile, ip, ports, protocol string) (w *Workload, err error) {
	workloadIdx++
	n := fmt.Sprintf("%s-idx%v", name, workloadIdx)
	interfaceName := conversion.VethNameForWorkload(profile, n)
	if c.IP == ip {
		interfaceName = ""
	}
	// Build unique workload name and struct.
	workloadIdx++
	w = &Workload{
		C:             c.Container,
		Name:          n,
		InterfaceName: interfaceName,
		IP:            ip,
		Ports:         ports,
		Protocol:      protocol,
	}

	// Ensure that the host has the 'test-workload' binary.
	w.C.EnsureBinary("test-workload")

	// Start the workload.
	log.WithField("workload", w).Info("About to run workload")
	var udpArg string
	if protocol == "udp" {
		udpArg = "--udp"
	}
	w.runCmd = utils.Command("docker", "exec", w.C.Name,
		"sh", "-c",
		fmt.Sprintf("echo $$ > /tmp/%v; exec /test-workload %v '%v' '%v' '%v'",
			w.Name,
			udpArg,
			w.InterfaceName,
			w.IP,
			w.Ports))
	w.outPipe, err = w.runCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("Getting StdoutPipe failed: %v", err)
	}
	w.errPipe, err = w.runCmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("Getting StderrPipe failed: %v", err)
	}
	err = w.runCmd.Start()
	if err != nil {
		return nil, fmt.Errorf("runCmd Start failed: %v", err)
	}

	// Read the workload's namespace path, which it writes to its standard output.
	stdoutReader := bufio.NewReader(w.outPipe)
	stderrReader := bufio.NewReader(w.errPipe)

	var errDone sync.WaitGroup
	errDone.Add(1)
	go func() {
		defer errDone.Done()
		for {
			line, err := stderrReader.ReadString('\n')
			if err != nil {
				log.WithError(err).Info("End of workload stderr")
				return
			}
			log.Infof("Workload %s stderr: %s", name, strings.TrimSpace(string(line)))
		}
	}()

	namespacePath, err := stdoutReader.ReadString('\n')
	if err != nil {
		// (Only) if we fail here, wait for the stderr to be output before returning.
		defer errDone.Wait()
		if err != nil {
			return nil, fmt.Errorf("Reading from stdout failed: %v", err)
		}
	}

	w.namespacePath = strings.TrimSpace(namespacePath)

	go func() {
		for {
			line, err := stdoutReader.ReadString('\n')
			if err != nil {
				log.WithError(err).Info("End of workload stdout")
				return
			}
			log.Infof("Workload %s stdout: %s", name, strings.TrimSpace(string(line)))
		}
	}()

	log.WithField("workload", w).Info("Workload now running")

	wep := api.NewWorkloadEndpoint()
	wep.Labels = map[string]string{"name": w.Name}
	wep.Spec.Node = w.C.Hostname
	wep.Spec.Orchestrator = "felixfv"
	wep.Spec.Workload = w.Name
	wep.Spec.Endpoint = w.Name
	prefixLen := "32"
	if strings.Contains(w.IP, ":") {
		prefixLen = "128"
	}
	wep.Spec.IPNetworks = []string{w.IP + "/" + prefixLen}
	wep.Spec.InterfaceName = w.InterfaceName
	wep.Spec.Profiles = []string{profile}
	w.WorkloadEndpoint = wep

	return w, nil
}

func (w *Workload) IPNet() string {
	return w.IP + "/32"
}

func (w *Workload) Configure(client client.Interface) {
	wep := w.WorkloadEndpoint
	wep.Namespace = "fv"
	var err error
	w.WorkloadEndpoint, err = client.WorkloadEndpoints().Create(utils.Ctx, w.WorkloadEndpoint, utils.NoOptions)
	Expect(err).NotTo(HaveOccurred())
}

func (w *Workload) RemoveFromDatastore(client client.Interface) {
	_, err := client.WorkloadEndpoints().Delete(utils.Ctx, "fv", w.WorkloadEndpoint.Name, options.DeleteOptions{})
	Expect(err).NotTo(HaveOccurred())
}

func (w *Workload) ConfigureInDatastore(infra infrastructure.DatastoreInfra) {
	wep := w.WorkloadEndpoint
	wep.Namespace = "default"
	var err error
	w.WorkloadEndpoint, err = infra.AddWorkload(wep)
	Expect(err).NotTo(HaveOccurred(), "Failed to add workload")
}

func (w *Workload) NameSelector() string {
	return "name=='" + w.Name + "'"
}

func (w *Workload) SourceName() string {
	return w.Name
}

func (w *Workload) CanConnectTo(ip, port, protocol string, duration time.Duration) (bool, string) {
	anyPort := Port{
		Workload: w,
	}
	return anyPort.CanConnectTo(ip, port, protocol, duration)
}

func (w *Workload) Port(port uint16) *Port {
	return &Port{
		Workload: w,
		Port:     port,
	}
}

type Port struct {
	*Workload
	Port uint16
}

func (w *Port) SourceName() string {
	if w.Port == 0 {
		return w.Name
	}
	return fmt.Sprintf("%s:%d", w.Name, w.Port)
}

func (w *Workload) NamespaceID() string {
	splits := strings.Split(w.namespacePath, "/")
	return splits[len(splits)-1]
}

func (w *Workload) ExecOutput(args ...string) (string, error) {
	args = append([]string{"ip", "netns", "exec", w.NamespaceID()}, args...)
	return w.C.ExecOutput(args...)
}

func (w *Workload) ExecCombinedOutput(args ...string) (string, error) {
	args = append([]string{"ip", "netns", "exec", w.NamespaceID()}, args...)
	return w.C.ExecCombinedOutput(args...)
}

var (
	rttRegexp = regexp.MustCompile(`rtt=(.*) ms`)
)

func (w *Workload) LatencyTo(ip, port string) (time.Duration, string) {
	if strings.Contains(ip, ":") {
		ip = fmt.Sprintf("[%s]", ip)
	}
	out, err := w.ExecOutput("hping3", "-p", port, "-c", "20", "--fast", "-S", "-n", ip)
	stderr := ""
	if err, ok := err.(*exec.ExitError); ok {
		stderr = string(err.Stderr)
	}
	Expect(err).NotTo(HaveOccurred(), stderr)

	lines := strings.Split(out, "\n")[1:] // Skip header line
	var rttSum time.Duration
	var numBuggyRTTs int
	for _, line := range lines {
		if len(line) == 0 {
			continue
		}
		matches := rttRegexp.FindStringSubmatch(line)
		Expect(matches).To(HaveLen(2), "Failed to extract RTT from line: "+line)
		rttMsecStr := matches[1]
		rttMsec, err := strconv.ParseFloat(rttMsecStr, 64)
		Expect(err).ToNot(HaveOccurred())
		if rttMsec > 1000 {
			// There's a bug in hping where it occasionally reports RTT+1s instead of RTT.  Work around that
			// but keep track of the number of workarounds and bail out if we see too many.
			rttMsec -= 1000
			numBuggyRTTs++
		}
		rttSum += time.Duration(rttMsec * float64(time.Millisecond))
	}
	Expect(numBuggyRTTs).To(BeNumerically("<", len(lines)/2),
		"hping reported a large number of >1s RTTs; full output:\n"+out)
	meanRtt := rttSum / time.Duration(len(lines))
	return meanRtt, out
}

type SideService struct {
	W       *Workload
	Name    string
	RunCmd  *exec.Cmd
	PidFile string
}

func (s *SideService) Stop() {
	Expect(s.stop()).NotTo(HaveOccurred())
}

func (s *SideService) stop() error {
	log.WithField("SideService", s).Info("Stop")
	output, err := s.W.C.ExecOutput("cat", s.PidFile)
	if err != nil {
		log.WithField("pidfile", s.PidFile).WithError(err).Warn("Failed to get contents of a side service's pidfile")
		return err
	}
	pid := strings.TrimSpace(output)
	err = s.W.C.ExecMayFail("kill", pid)
	if err != nil {
		log.WithField("pid", pid).WithError(err).Warn("Failed to kill a side service")
		return err
	}
	_, err = s.RunCmd.Process.Wait()
	if err != nil {
		log.WithField("side service", s).Error("failed to wait for process")
	}

	log.WithField("SideService", s).Info("Side service now stopped")
	return nil
}

func (w *Workload) StartSideService() *SideService {
	s, err := startSideService(w)
	Expect(err).NotTo(HaveOccurred())
	return s
}

func startSideService(w *Workload) (*SideService, error) {
	// Ensure that the host has the 'test-workload' binary.
	w.C.EnsureBinary("test-workload")
	sideServIdx++
	n := fmt.Sprintf("%s-ss%d", w.Name, sideServIdx)
	pidFile := fmt.Sprintf("/tmp/%s-pid", n)

	testWorkloadShArgs := []string{
		"/test-workload",
	}
	if w.Protocol == "udp" {
		testWorkloadShArgs = append(testWorkloadShArgs, "--udp")
	}
	testWorkloadShArgs = append(testWorkloadShArgs,
		"--sidecar-iptables",
		"--up-lo",
		fmt.Sprintf("'--namespace-path=%s'", w.namespacePath),
		"''", // interface name, not important
		"127.0.0.1",
		"15001",
	)
	pidCmd := fmt.Sprintf("echo $$ >'%s'", pidFile)
	testWorkloadCmd := strings.Join(testWorkloadShArgs, " ")
	dockerWorkloadArgs := []string{
		"docker",
		"exec",
		w.C.Name,
		"sh", "-c",
		fmt.Sprintf("%s; exec %s", pidCmd, testWorkloadCmd),
	}
	runCmd := utils.Command(dockerWorkloadArgs[0], dockerWorkloadArgs[1:]...)
	logName := fmt.Sprintf("side service %s", n)
	if err := utils.LogOutput(runCmd, logName); err != nil {
		return nil, fmt.Errorf("failed to start output logging for %s", logName)
	}
	if err := runCmd.Start(); err != nil {
		return nil, fmt.Errorf("starting /test-workload as a side service failed: %v", err)
	}
	return &SideService{
		W:       w,
		Name:    n,
		RunCmd:  runCmd,
		PidFile: pidFile,
	}, nil
}

type PermanentConnection struct {
	W        *Workload
	LoopFile string
	Name     string
	RunCmd   *exec.Cmd
}

func (pc *PermanentConnection) Stop() {
	Expect(pc.stop()).NotTo(HaveOccurred())
}

func (pc *PermanentConnection) stop() error {
	if err := pc.W.C.ExecMayFail("sh", "-c", fmt.Sprintf("echo > %s", pc.LoopFile)); err != nil {
		log.WithError(err).WithField("loopfile", pc.LoopFile).Warn("Failed to create a loop file to stop the permanent connection")
		return err
	}
	if err := pc.RunCmd.Wait(); err != nil {
		return err
	}
	return nil
}

func (w *Workload) StartPermanentConnection(ip string, port, sourcePort int) *PermanentConnection {
	pc, err := startPermanentConnection(w, ip, port, sourcePort)
	Expect(err).NotTo(HaveOccurred())
	return pc
}

func startPermanentConnection(w *Workload, ip string, port, sourcePort int) (*PermanentConnection, error) {
	// Ensure that the host has the 'test-connection' binary.
	w.C.EnsureBinary("test-connection")
	permConnIdx++
	n := fmt.Sprintf("%s-pc%d", w.Name, permConnIdx)
	loopFile := fmt.Sprintf("/tmp/%s-loop", n)

	err := w.C.ExecMayFail("sh", "-c", fmt.Sprintf("echo > %s", loopFile))
	if err != nil {
		return nil, err
	}

	runCmd := utils.Command(
		"docker",
		"exec",
		w.C.Name,
		"/test-connection",
		w.namespacePath,
		ip,
		fmt.Sprintf("%d", port),
		fmt.Sprintf("--source-port=%d", sourcePort),
		fmt.Sprintf("--protocol=%s", w.Protocol),
		fmt.Sprintf("--loop-with-file=%s", loopFile),
	)
	logName := fmt.Sprintf("permanent connection %s", n)
	if err := utils.LogOutput(runCmd, logName); err != nil {
		return nil, fmt.Errorf("failed to start output logging for %s", logName)
	}
	if err := runCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start a permanent connection: %v", err)
	}
	Eventually(func() error {
		return w.C.ExecMayFail("stat", loopFile)
	}, 5*time.Second, time.Second).Should(
		HaveOccurred(),
		"Failed to wait for test-connection to be ready, the loop file did not disappear",
	)
	return &PermanentConnection{
		W:        w,
		LoopFile: loopFile,
		Name:     n,
		RunCmd:   runCmd,
	}, nil
}

func (p *Port) CanConnectTo(ip, port, protocol string) bool {
	// Ensure that the host has the 'test-connection' binary.
	p.C.EnsureBinary("test-connection")

	if protocol == "udp" {
		// If this is a retry then we may have stale conntrack entries and we don't want those
		// to influence the connectivity check.  Only an issue for UDP due to the lack of a
		// sequence number.
		_ = p.C.ExecMayFail("conntrack", "-D", "-p", "udp", "-s", p.Workload.IP, "-d", ip)
	}

	// Run 'test-connection' to the target.
	args := []string{
		"exec", p.C.Name, "/test-connection", p.namespacePath, ip, port, "--protocol=" + protocol, fmt.Sprintf("--duration=%d", int(duration.Seconds())),
	}
	if p.Port != 0 {
		// If we are using a particular source port, fill it in.
		args = append(args, fmt.Sprintf("--source-port=%d", p.Port))
	}
	connectionCmd := utils.Command("docker", args...)
	outPipe, err := connectionCmd.StdoutPipe()
	Expect(err).NotTo(HaveOccurred())
	errPipe, err := connectionCmd.StderrPipe()
	Expect(err).NotTo(HaveOccurred())
	err = connectionCmd.Start()
	Expect(err).NotTo(HaveOccurred())

	return utils.RunConnectionCmd(connectionCmd)
}

// ToMatcher implements the connectionTarget interface, allowing this port to be used as
// target.
func (p *Port) ToMatcher(explicitPort ...uint16) *connectivityMatcher {
	if p.Port == 0 {
		return p.Workload.ToMatcher(explicitPort...)
	}
	return &connectivityMatcher{
		ip:         p.Workload.IP,
		port:       fmt.Sprint(p.Port),
		targetName: fmt.Sprintf("%s on port %d", p.Workload.Name, p.Port),
	}
}

type connectionTarget interface {
	ToMatcher(explicitPort ...uint16) *connectivityMatcher
}

type IP string // Just so we can define methods on it...

func (s IP) ToMatcher(explicitPort ...uint16) *connectivityMatcher {
	if len(explicitPort) != 1 {
		panic("Explicit port needed with IP as a connectivity target")
	}
	port := fmt.Sprintf("%d", explicitPort[0])
	return &connectivityMatcher{
		ip:         string(s),
		port:       port,
		targetName: string(s) + ":" + port,
		protocol:   "tcp",
	}
}

func (w *Workload) ToMatcher(explicitPort ...uint16) *connectivityMatcher {
	var port string
	if len(explicitPort) == 1 {
		port = fmt.Sprintf("%d", explicitPort[0])
	} else if w.DefaultPort != "" {
		port = w.DefaultPort
	} else if !strings.Contains(w.Ports, ",") {
		port = w.Ports
	} else {
		panic("Explicit port needed for workload with multiple ports")
	}
	return &connectivityMatcher{
		ip:         w.IP,
		port:       port,
		targetName: fmt.Sprintf("%s on port %s", w.Name, port),
		protocol:   "tcp",
	}
}

func HaveConnectivityTo(target connectionTarget, explicitPort ...uint16) types.GomegaMatcher {
	return target.ToMatcher(explicitPort...)
}

type connectivityMatcher struct {
	ip, port, targetName, protocol string
}

type connectionSource interface {
	CanConnectTo(ip, port, protocol string, duration time.Duration) (bool, string)
	SourceName() string
}

func (m *connectivityMatcher) Match(actual interface{}) (success bool, err error) {
	success, _ = actual.(connectionSource).CanConnectTo(m.ip, m.port, m.protocol, time.Duration(0))
	return
}

func (m *connectivityMatcher) FailureMessage(actual interface{}) (message string) {
	src := actual.(connectionSource)
	message = fmt.Sprintf("Expected %v\n\t%+v\nto have connectivity to %v\n\t%v:%v\nbut it does not", src.SourceName(), src, m.targetName, m.ip, m.port)
	return
}

func (m *connectivityMatcher) NegatedFailureMessage(actual interface{}) (message string) {
	src := actual.(connectionSource)
	message = fmt.Sprintf("Expected %v\n\t%+v\nnot to have connectivity to %v\n\t%v:%v\nbut it does", src.SourceName(), src, m.targetName, m.ip, m.port)
	return
}

type expPacketLoss struct {
	duration   time.Duration // how long test will run
	maxPercent int           // 10 means 10%. -1 means field not valid.
	maxNumber  int           // 10 means 10 packets. -1 means field not valid.
}

type expectation struct {
	from               connectionSource     // Workload or Container
	to                 *connectivityMatcher // Workload or IP, + port
	expected           bool
	expectedPacketLoss expPacketLoss
}

var UnactivatedConnectivityCheckers = set.New()

// ConnectivityChecker records a set of connectivity expectations and supports calculating the
// actual state of the connectivity between the given workloads.  It is expected to be used like so:
//
//     var cc = &workload.ConnectivityChecker{}
//     cc.ExpectNone(w[2], w[0], 1234)
//     cc.ExpectSome(w[1], w[0], 5678)
//     Eventually(cc.ActualConnectivity, "10s", "100ms").Should(Equal(cc.ExpectedConnectivity()))
//
// Note that the ActualConnectivity method is passed to Eventually as a function pointer to allow
// Ginkgo to re-evaluate the result as needed.
type ConnectivityChecker struct {
	ReverseDirection bool
	Protocol         string // "tcp" or "udp"
	expectations     []expectation
}

func (c *ConnectivityChecker) ExpectSome(from connectionSource, to connectionTarget, explicitPort ...uint16) {
	UnactivatedConnectivityCheckers.Add(c)
	if c.ReverseDirection {
		from, to = to.(connectionSource), from.(connectionTarget)
	}
	c.expectations = append(c.expectations, expectation{from: from, to: to.ToMatcher(explicitPort...), expected: true})
}

func (c *ConnectivityChecker) ExpectNone(from connectionSource, to connectionTarget, explicitPort ...uint16) {
	UnactivatedConnectivityCheckers.Add(c)
	if c.ReverseDirection {
		from, to = to.(connectionSource), from.(connectionTarget)
	}
	c.expectations = append(c.expectations, expectation{from: from, to: to.ToMatcher(explicitPort...), expected: false})
}

func (c *ConnectivityChecker) ExpectLoss(from connectionSource, to connectionTarget,
	duration time.Duration, maxPacketLossPercent, maxPacketLossNumber int, explicitPort ...uint16) {

	Expect(duration.Seconds()).NotTo(BeZero())
	Expect(maxPacketLossPercent).To(BeNumerically(">=", -1))
	Expect(maxPacketLossPercent).To(BeNumerically("<=", 100))
	Expect(maxPacketLossNumber).To(BeNumerically(">=", -1))
	Expect(maxPacketLossPercent + maxPacketLossNumber).NotTo(Equal(-2)) // Do not set both value to -1

	UnactivatedConnectivityCheckers.Add(c)
	if c.ReverseDirection {
		from, to = to.(connectionSource), from.(connectionTarget)
	}
	c.expectations = append(c.expectations, expectation{from, to.ToMatcher(explicitPort...), true,
		expPacketLoss{duration: duration, maxPercent: maxPacketLossPercent, maxNumber: maxPacketLossNumber}})
}

func (c *ConnectivityChecker) ResetExpectations() {
	c.expectations = nil
}

// ActualConnectivity calculates the current connectivity for all the expected paths.  One string is
// returned for each expectation, in the order they were recorded.  The strings are intended to be
// human readable, and they are in the same order and format as those returned by
// ExpectedConnectivity().
func (c *ConnectivityChecker) ActualConnectivity() []string {
	UnactivatedConnectivityCheckers.Discard(c)
	var wg sync.WaitGroup
	result := make([]string, len(c.expectations))
	for i, exp := range c.expectations {
		wg.Add(1)
		go func(i int, exp expectation) {
			defer wg.Done()
			p := "tcp"
			if c.Protocol != "" {
				p = c.Protocol
			}

			hasConnectivity, statString := exp.from.CanConnectTo(exp.to.ip, exp.to.port, p, exp.expectedPacketLoss.duration)
			result[i] = fmt.Sprintf("%s -> %s = %v %s", exp.from.SourceName(), exp.to.targetName, hasConnectivity, statString)

		}(i, exp)
	}
	wg.Wait()
	log.Debug("Connectivity", result)
	return result
}

// ExpectedConnectivity returns one string per recorded expection in order, encoding the expected.
// It also returns minimum retries for a connection check.
// connectivity in the same format used by ActualConnectivity().
func (c *ConnectivityChecker) ExpectedConnectivity() ([]string, int) {
	minRetries := 1 // Default retry once
	result := make([]string, len(c.expectations))
	for i, exp := range c.expectations {
		result[i] = fmt.Sprintf("%s -> %s = %v ", exp.from.SourceName(), exp.to.targetName, exp.expected)
		if exp.expectedPacketLoss.duration != 0 {
			result[i] += utils.FormPacketLossString(exp.expectedPacketLoss.maxPercent, exp.expectedPacketLoss.maxNumber)
			minRetries = 0 // Never retry for packet loss test.
		}
	}
	return result, minRetries
}

func (c *ConnectivityChecker) CheckConnectivityOffset(offset int, optionalDescription ...interface{}) {
	c.CheckConnectivityWithTimeoutOffset(offset+2, 10*time.Second, optionalDescription...)
}

func (c *ConnectivityChecker) CheckConnectivity(optionalDescription ...interface{}) {
	c.CheckConnectivityWithTimeoutOffset(2, 10*time.Second, optionalDescription...)
}

func (c *ConnectivityChecker) CheckConnectivityPacketLoss(optionalDescription ...interface{}) {
	// Timeout is not used for packet loss test because there is no retry.
	c.CheckConnectivityWithTimeoutOffset(2, 0*time.Second, optionalDescription...)
}

func (c *ConnectivityChecker) CheckConnectivityWithTimeout(timeout time.Duration, optionalDescription ...interface{}) {
	Expect(timeout).To(BeNumerically(">", 100*time.Millisecond),
		"Very low timeout, did you mean to multiply by time.<Unit>?")
	if len(optionalDescription) > 0 {
		Expect(optionalDescription[0]).NotTo(BeAssignableToTypeOf(time.Second),
			"Unexpected time.Duration passed for description")
	}
	c.CheckConnectivityWithTimeoutOffset(2, timeout, optionalDescription...)
}

func (c *ConnectivityChecker) CheckConnectivityWithTimeoutOffset(callerSkip int, timeout time.Duration, optionalDescription ...interface{}) {
	expConnectivity, minRetries := c.ExpectedConnectivity()
	start := time.Now()

	// Track the number of attempts for non packet loss test. If the first connectivity check fails, we want to
	// do at least one retry before we time out.  That covers the case where the first
	// connectivity check takes longer than the timeout.
	// For packet loss test, no retry.
	completedAttempts := 0
	var errMsg string

	for time.Since(start) < timeout || completedAttempts <= minRetries { // use 'or' here to make sure we do retry.

		actualConn := c.ActualConnectivity()
		errMsg = actualSatisfyExpected(actualConn, expConnectivity)
		if errMsg == "" {
			return
		}
		completedAttempts++
	}

	Fail(errMsg, callerSkip)
}

// Check test results against expectations.
// Return non-empty error messages if any test failed.
func actualSatisfyExpected(actual, expected []string) string {
	Expect(len(expected)).To(Equal(len(actual)))
	// run a deep equal first. Tests are good if everything is equal.
	if reflect.DeepEqual(actual, expected) {
		return ""
	}

	// Make a copy of expected. We do not want to change it because the test could retry.
	expMsgs := make([]string, len(expected))
	copy(expMsgs, expected)

	good := true
	// Build a concise description of the incorrect connectivity if test failed.
	for i := range expected {
		if !strings.Contains(expected[i], utils.PacketLossPrefix) {
			if actual[i] != expected[i] {
				actual[i] += " <---- WRONG"
				expMsgs[i] += " <----"
				good = false
			}
		} else {
			// expected got a packet loss string.
			Expect(actual[i]).To(ContainSubstring(utils.PacketTotalReqPrefix))

			// Check packet loss.
			actualPercent, actualNumber := utils.GetPacketLossFromStat(actual[i])
			expPercent, expNumber := utils.GetPacketLossDirect(expected[i])

			if (expPercent >= 0 && actualPercent > expPercent) || (expNumber >= 0 && actualNumber > expNumber) {
				actual[i] += " <---- WRONG:" + utils.FormPacketLossString(actualPercent, actualNumber)
				expMsgs[i] += " <----"
				good = false
			}
		}
	}

	message := ""
	if !good {
		message = fmt.Sprintf(
			"Connectivity was incorrect:\n\nExpected\n    %s\nto match\n    %s",
			strings.Join(actual, "\n    "),
			strings.Join(expMsgs, "\n    "),
		)
	}

	return message
}
