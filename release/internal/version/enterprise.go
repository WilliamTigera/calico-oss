package version

import "fmt"

func NewEnterpriseVersionData(calico Version, chartVersion, operator, manager string) Data {
	return &EnterpriseVersionData{
		CalicoVersionData: CalicoVersionData{
			calico:   calico,
			operator: operator,
		},
		chartVersion: chartVersion,
		manager:      manager,
	}
}

type EnterpriseVersionData struct {
	CalicoVersionData
	chartVersion string
	manager      string
	release      bool
}

func (v *EnterpriseVersionData) ChartVersion() string {
	return v.chartVersion
}

func (v *EnterpriseVersionData) HelmChartVersion() string {
	if v.chartVersion == "" {
		return v.calico.FormattedString()
	}
	return fmt.Sprintf("%s-%s", v.calico.FormattedString(), v.chartVersion)
}

func (v *EnterpriseVersionData) OperatorVersion() string {
	if v.release {
		return v.operator
	}
	return fmt.Sprintf("%s-%s", v.operator, v.calico.FormattedString())
}

func (v *EnterpriseVersionData) Hash() string {
	return fmt.Sprintf("%s-%s-%s", v.calico.FormattedString(), v.operator, v.manager)
}

func (v *EnterpriseVersionData) ManagerVersion() string {
	return v.manager
}

func (v *EnterpriseVersionData) ForRelease() *EnterpriseVersionData {
	v.release = true
	return v
}
