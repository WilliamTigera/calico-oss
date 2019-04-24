package scaleloader

import (
	"bytes"
	"fmt"
	"io/ioutil"
	"reflect"
	"text/template"

	json "github.com/projectcalico/go-json/json"
	yaml "github.com/projectcalico/go-yaml-wrapper"

	logrus "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/tigera/compliance/pkg/resources"
)

type Play struct {
	pathSrc      string
	namespace    string
	playInstance int
	steps        []Step
	init         []Step
}

func (p *Play) GetSteps(playIteration int) []Step {
	steps := []Step{}
	for i := range p.steps {
		step := p.steps[i]
		step.namespace = p.namespace
		step.playIteration = playIteration
		step.playInstance = p.playInstance
		steps = append(steps, step.Clone())
	}

	return steps
}

func (p *Play) Clone() Play {
	n := Play{
		pathSrc:      p.pathSrc,
		namespace:    p.namespace,
		playInstance: p.playInstance,
	}

	for i := range p.steps {
		n.steps = append(n.steps, p.steps[i].Clone())
	}
	for i := range p.init {
		n.init = append(n.init, p.init[i].Clone())
	}
	return n
}

type Step struct {
	resource      resources.Resource
	Action        string
	scaleName     string
	resName       string
	stepCount     int
	namespace     string
	playIteration int
	playInstance  int
}

type StepAction struct {
	Action string `json:"action,omitempty"`
}

func NewStep(name string, scaleName string, step int, i resources.Resource, action string) Step {
	return Step{
		resource:  i,
		Action:    action,
		scaleName: scaleName,
		stepCount: step,
		resName:   name,
	}
}

func (s *Step) Clone() Step {
	n := *s
	rh := resources.GetResourceHelper(s.GetTypeMeta())
	if rh == nil {
		logrus.Fatal("Failed to get resource helper to clone step")
		return n
	}

	n.resource = s.resource.DeepCopyObject().(resources.Resource)
	return n
}

type Meta struct {
	metav1.ObjectMeta `json:"metadata"`
}

func readStep(log *logrus.Entry, scaleName string, stepCount int, path string) (Step, error) {
	step := Step{}

	data, err := ioutil.ReadFile(path)
	if err != nil {
		log.WithError(err).WithField("path", path).Error("failed to read yaml")
		return Step{}, fmt.Errorf("Failed to read %s: %v", path, err)
	}

	// Extract group version kind from event response object.
	var kind metav1.TypeMeta
	if err := yaml.Unmarshal(data, &kind); err != nil {
		log.WithError(err).WithField("yaml", string(data)).Error("failed to unmarshal yaml")
		return step, err
	}

	// Extract resource from event response object.
	clog := log.WithFields(logrus.Fields{"type": kind})
	rh := resources.GetResourceHelper(kind)
	if rh == nil {
		clog.WithField("yaml", string(data)).Warn("Failed to get resource helper")
		return step, nil
	}

	res := rh.NewResource()
	if err := yaml.Unmarshal(data, res); err != nil {
		clog.WithError(err).WithField("json", string(data)).Error("failed to unmarshal yaml")
		return step, err
	}

	var meta Meta
	if err := yaml.Unmarshal(data, &meta); err != nil {
		log.WithError(err).WithField("yaml", string(data)).Error("failed to unmarshal yaml")
		return step, err
	}
	clog = clog.WithField("name", meta.Name)

	var a StepAction
	if err := yaml.Unmarshal(data, &a); err != nil {
		clog.WithError(err).WithField("json", string(data)).Error("failed to unmarshal yaml")
		return step, err
	}

	step = NewStep(meta.Name, scaleName, stepCount, res, a.Action)

	return step, nil
}

func (s *Step) GetIndex() string {
	switch s.GetTypeMeta() {
	case resources.TypeK8sPods, resources.TypeK8sNetworkPolicies,
		resources.TypeK8sServices, resources.TypeK8sEndpoints,
		resources.TypeK8sNamespaces, resources.TypeK8sNetworkPoliciesExtensions,
		resources.TypeK8sServiceAccounts:
		return "tigera_secure_ee_audit_kube"
	case resources.TypeCalicoGlobalNetworkPolicies, resources.TypeCalicoGlobalNetworkSets,
		resources.TypeCalicoHostEndpoints, resources.TypeCalicoNetworkPolicies,
		resources.TypeCalicoTiers:
		return "tigera_secure_ee_audit_ee"
	default:
		logrus.Fatalf("Unsupported resource %s", s.GetTypeMeta())
		return ""
	}
}

func (s *Step) GetMsg(revision int, timestamp string) (string, error) {
	switch s.GetTypeMeta() {
	case resources.TypeK8sPods, resources.TypeK8sNetworkPolicies,
		resources.TypeK8sServices, resources.TypeK8sEndpoints,
		resources.TypeK8sNamespaces, resources.TypeK8sNetworkPoliciesExtensions,
		resources.TypeK8sServiceAccounts:

		return s.getAuditV1(revision, timestamp)

	case resources.TypeCalicoGlobalNetworkPolicies, resources.TypeCalicoGlobalNetworkSets,
		resources.TypeCalicoHostEndpoints, resources.TypeCalicoNetworkPolicies,
		resources.TypeCalicoTiers:

		return s.getAuditV1Beta(revision, timestamp)
	}

	return "", fmt.Errorf("Unexpected kind %s", s.GetTypeMeta())
}

func (s *Step) String() string {
	return fmt.Sprintf("%s %s/%s-%d/%s/%s:s%d",
		s.getAction(),
		s.namespace,
		s.scaleName,
		s.playInstance,
		s.GetTypeMeta().Kind,
		s.resolveResourceTemplate(s.resName),
		s.stepCount,
	)
}

func (s *Step) GetTypeMeta() metav1.TypeMeta {
	return resources.GetTypeMeta(s.resource)
}

func (s *Step) getAction() string {
	if s.Action == "" {
		return "update"
	}
	return s.Action
}

func (s *Step) getAuditV1(revision int, timestamp string) (string, error) {
	s.UpdateResource(revision)

	j, err := json.Marshal(s.resource)
	if err != nil {
		return "", fmt.Errorf("error marshaling into JSON: %v", err)
	}

	return Tprintf(auditV1Template, map[string]interface{}{
		"Verb":           s.getAction(),
		"ResponseObject": string(j),
		"Timestamp":      timestamp,
	}), nil
}

func (s *Step) getAuditV1Beta(revision int, timestamp string) (string, error) {
	s.UpdateResource(revision)

	j, err := json.Marshal(s.resource)
	if err != nil {
		return "", fmt.Errorf("error marshaling into JSON: %v", err)
	}

	return Tprintf(auditV1BetaTemplate, map[string]interface{}{
		"Verb":           s.getAction(),
		"ResponseObject": string(j),
		"Timestamp":      timestamp,
	}), nil
}

func (s *Step) UpdateResource(revision int) {

	rev := fmt.Sprintf("%d", revision)
	if s.GetTypeMeta() == resources.TypeCalicoNetworkPolicies {
		rev = fmt.Sprintf("%d/", revision)
	}
	s.resource.GetObjectMeta().SetResourceVersion(rev)

	v := reflect.Indirect(reflect.ValueOf(s.resource))
	s.updateResource(v)
}

func (s *Step) updateResource(valueOf reflect.Value) {
	switch valueOf.Kind() {
	case reflect.Struct:

		for i := 0; i < valueOf.NumField(); i++ {
			s.updateResource(valueOf.Field(i))
		}
	case reflect.Slice:
		fallthrough
	case reflect.Array:
		for j := 0; j < valueOf.Len(); j++ {
			s.updateResource(valueOf.Index(j))
		}
	case reflect.Map:
		for _, key := range valueOf.MapKeys() {
			s.updateResource(valueOf.MapIndex(key))
		}
	case reflect.Ptr:
		s.updateResource(reflect.Indirect(valueOf))

	}

	if valueOf.IsValid() {
		if valueOf.Kind() == reflect.String {
			str := s.resolveResourceTemplate(valueOf.String())
			//logrus.WithField("resolveT", str).Debug("Resolved")
			if valueOf.CanSet() {
				valueOf.SetString(str)
			}
		}
	}
}

func (s *Step) resolveResourceTemplate(t string) string {
	return Tprintf(t, map[string]interface{}{
		"Namespace": s.namespace,
		"ScaleId":   fmt.Sprintf("%d-%d", s.playInstance, s.playIteration),
	})
}

// Tprintf passed template string is formatted usign its operands and returns the resulting string.
// Spaces are added between operands when neither is a string.
func Tprintf(tmpl string, data map[string]interface{}) string {
	t := template.Must(template.New("sql").Parse(tmpl))
	buf := &bytes.Buffer{}
	if err := t.Execute(buf, data); err != nil {
		return ""
	}
	return buf.String()
}
