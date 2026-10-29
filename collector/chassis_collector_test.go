package collector

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stmcginnis/gofish"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	mockServiceRoot = `{"@odata.type":"#ServiceRoot.v1_15_0.ServiceRoot","@odata.id":"/redfish/v1/","Id":"RootService","Name":"Root Service","RedfishVersion":"1.15.0","Chassis":{"@odata.id":"/redfish/v1/Chassis"}}`

	mockChassisCollection = `{"@odata.type":"#ChassisCollection.ChassisCollection","@odata.id":"/redfish/v1/Chassis","Name":"Chassis Collection","Members@odata.count":2,"Members":[{"@odata.id":"/redfish/v1/Chassis/1"},{"@odata.id":"/redfish/v1/Chassis/NVMeSSD.0.Group.0.StorageBackplane"}]}`

	// A chassis with neither Power nor PowerSubsystem, as exposed for drive
	// backplanes by Supermicro BMCs.
	mockBackplaneChassis = `{"@odata.type":"#Chassis.v1_25_0.Chassis","@odata.id":"/redfish/v1/Chassis/NVMeSSD.0.Group.0.StorageBackplane","Id":"NVMeSSD.0.Group.0.StorageBackplane","Name":"NVMe Storage Backplane","ChassisType":"StorageEnclosure","Status":{"State":"Enabled","Health":"OK"}}`

	// Newer firmware: only the PowerSubsystem resource exists. Shaped after the
	// responses of a Supermicro X13 BMC (sjc-prod-r2205m01).
	mockChassisPowerSubsystem = `{"@odata.type":"#Chassis.v1_25_0.Chassis","@odata.id":"/redfish/v1/Chassis/1","Id":"1","Name":"Computer System Chassis","ChassisType":"RackMount","Manufacturer":"Supermicro","Model":"X13DEG-OA","Status":{"State":"Enabled","Health":"OK"},"PowerSubsystem":{"@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem"},"ThermalSubsystem":{"@odata.id":"/redfish/v1/Chassis/1/ThermalSubsystem"}}`
	mockPowerSubsystem        = `{"@odata.type":"#PowerSubsystem.v1_1_0.PowerSubsystem","@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem","Id":"PowerSubsystem","Name":"Power Subsystem","CapacityWatts":2400,"PowerSupplies":{"@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies"},"Status":{"State":"Enabled","Health":"OK"}}`
	mockPowerSupplies         = `{"@odata.type":"#PowerSupplyCollection.PowerSupplyCollection","@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies","Name":"PowerSupplyCollection","Members@odata.count":2,"Members":[{"@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/1"},{"@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/2"}]}`
	mockPowerSupply1          = `{"@odata.type":"#PowerSupply.v1_5_0.PowerSupply","@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/1","Id":"1","Name":"Power Supply Bay 1","Status":{"State":"Enabled","Health":"OK"},"Manufacturer":"SUPERMICRO","Model":"PWS-2K08A-1R","SerialNumber":"P2K8ACH12AB0001","PowerCapacityWatts":2000,"EfficiencyRatings":[{"LoadPercent":50,"EfficiencyPercent":94},{"LoadPercent":100,"EfficiencyPercent":91}],"Metrics":{"@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/1/Metrics"}}`
	mockPowerSupply1Metrics   = `{"@odata.type":"#PowerSupplyMetrics.v1_1_0.PowerSupplyMetrics","@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/1/Metrics","Id":"Metrics","Name":"Power Supply Bay 1 Metrics","Status":{"State":"Enabled","Health":"OK"},"InputVoltage":{"Reading":208.5},"InputPowerWatts":{"Reading":533},"OutputPowerWatts":{"Reading":513}}`
	// PSU2 is degraded and its metrics resource carries no readings.
	mockPowerSupply2        = `{"@odata.type":"#PowerSupply.v1_5_0.PowerSupply","@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/2","Id":"2","Name":"Power Supply Bay 2","Status":{"State":"Enabled","Health":"Warning"},"Manufacturer":"SUPERMICRO","Model":"PWS-2K08A-1R","SerialNumber":"P2K8ACH12AB0002","PowerCapacityWatts":2000,"Metrics":{"@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/2/Metrics"}}`
	mockPowerSupply2Metrics = `{"@odata.type":"#PowerSupplyMetrics.v1_1_0.PowerSupplyMetrics","@odata.id":"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/2/Metrics","Id":"Metrics","Name":"Power Supply Bay 2 Metrics","Status":{"State":"Enabled","Health":"Warning"}}`

	// Older firmware: the deprecated Power resource with inline power supplies.
	// Note that gofish fails to parse a Power resource without a PowerControl
	// array ("unexpected end of JSON input").
	mockChassisLegacyPower = `{"@odata.type":"#Chassis.v1_14_0.Chassis","@odata.id":"/redfish/v1/Chassis/1","Id":"1","Name":"Computer System Chassis","ChassisType":"RackMount","Manufacturer":"Supermicro","Model":"X12DPG-QT6","Status":{"State":"Enabled","Health":"OK"},"Power":{"@odata.id":"/redfish/v1/Chassis/1/Power"},"Thermal":{"@odata.id":"/redfish/v1/Chassis/1/Thermal"}}`
	mockLegacyPower        = `{"@odata.type":"#Power.v1_7_0.Power","@odata.id":"/redfish/v1/Chassis/1/Power","Id":"Power","Name":"Power","PowerControl":[{"@odata.id":"/redfish/v1/Chassis/1/Power#/PowerControl/0","MemberId":"0","Name":"System Power Control","PowerConsumedWatts":420,"PowerMetrics":{"AverageConsumedWatts":415,"MaxConsumedWatts":610,"MinConsumedWatts":380}}],"PowerSupplies@odata.count":2,"PowerSupplies":[{"@odata.id":"/redfish/v1/Chassis/1/Power#/PowerSupplies/0","MemberId":"0","Name":"Power Supply Bay 1","Status":{"State":"Enabled","Health":"OK"},"PowerCapacityWatts":1200,"EfficiencyPercent":94,"LastPowerOutputWatts":210,"PowerInputWatts":231,"PowerOutputWatts":216},{"@odata.id":"/redfish/v1/Chassis/1/Power#/PowerSupplies/1","MemberId":"1","Name":"Power Supply Bay 2","Status":{"State":"Enabled","Health":"Critical"},"PowerCapacityWatts":1200,"PowerInputWatts":0,"PowerOutputWatts":0}]}`
	mockLegacyThermal      = `{"@odata.type":"#Thermal.v1_7_0.Thermal","@odata.id":"/redfish/v1/Chassis/1/Thermal","Id":"Thermal","Name":"Thermal","Temperatures":[{"@odata.id":"/redfish/v1/Chassis/1/Thermal#/Temperatures/0","MemberId":"0","Name":"CPU1 Temp","Status":{"State":"Enabled","Health":"OK"},"ReadingCelsius":45}],"Fans":[{"@odata.id":"/redfish/v1/Chassis/1/Thermal#/Fans/0","MemberId":"0","Name":"FAN1","Status":{"State":"Enabled","Health":"OK"},"Reading":5600,"ReadingUnits":"RPM","MinReadingRange":0,"MaxReadingRange":12000,"LowerThresholdCritical":700}]}`
)

// newRedfishMock serves a static Redfish resource tree over plain HTTP.
func newRedfishMock(t *testing.T, resources map[string]string) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := resources[strings.TrimSuffix(r.URL.Path, "/")]
		if !ok {
			t.Logf("mock BMC: no resource at %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// gatherPowerSupplyMetrics runs the chassis collector against srv and returns
// the power supply metric families, keyed by metric name and then by
// "chassis_id|power_supply_id|power_supply".
func gatherPowerSupplyMetrics(t *testing.T, srv *httptest.Server) map[string]map[string]float64 {
	client, err := gofish.Connect(gofish.ClientConfig{Endpoint: srv.URL, Username: "ADMIN", Password: "ADMIN", BasicAuth: true})
	require.NoError(t, err)
	t.Cleanup(client.Logout)

	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(NewChassisCollector(client, slog.New(slog.NewTextHandler(testLogWriter{t}, nil)))))
	families, err := reg.Gather()
	require.NoError(t, err)

	out := map[string]map[string]float64{}
	for _, family := range families {
		if !strings.HasPrefix(family.GetName(), "redfish_chassis_power_powersupply_") {
			continue
		}
		series := map[string]float64{}
		for _, m := range family.GetMetric() {
			key := fmt.Sprintf("%s|%s|%s", labelValue(m, "chassis_id"), labelValue(m, "power_supply_id"), labelValue(m, "power_supply"))
			series[key] = m.GetGauge().GetValue()
		}
		out[family.GetName()] = series
	}
	return out
}

// testLogWriter routes collector logs to the test log so failures show the
// Redfish errors behind missing metrics.
type testLogWriter struct{ t *testing.T }

func (w testLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}

func labelValue(m *dto.Metric, name string) string {
	for _, l := range m.GetLabel() {
		if l.GetName() == name {
			return l.GetValue()
		}
	}
	return ""
}

func TestChassisCollectorPowerSuppliesFromPowerSubsystem(t *testing.T) {
	srv := newRedfishMock(t, map[string]string{
		"/redfish/v1":         mockServiceRoot,
		"/redfish/v1/Chassis": mockChassisCollection,
		"/redfish/v1/Chassis/NVMeSSD.0.Group.0.StorageBackplane":       mockBackplaneChassis,
		"/redfish/v1/Chassis/1":                                        mockChassisPowerSubsystem,
		"/redfish/v1/Chassis/1/PowerSubsystem":                         mockPowerSubsystem,
		"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies":           mockPowerSupplies,
		"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/1":         mockPowerSupply1,
		"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/1/Metrics": mockPowerSupply1Metrics,
		"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/2":         mockPowerSupply2,
		"/redfish/v1/Chassis/1/PowerSubsystem/PowerSupplies/2/Metrics": mockPowerSupply2Metrics,
	})

	got := gatherPowerSupplyMetrics(t, srv)

	assert.Equal(t, map[string]map[string]float64{
		"redfish_chassis_power_powersupply_health":                      {"1|1|Power Supply Bay 1": 1, "1|2|Power Supply Bay 2": 2},
		"redfish_chassis_power_powersupply_state":                       {"1|1|Power Supply Bay 1": 1, "1|2|Power Supply Bay 2": 1},
		"redfish_chassis_power_powersupply_power_capacity_watts":        {"1|1|Power Supply Bay 1": 2000, "1|2|Power Supply Bay 2": 2000},
		"redfish_chassis_power_powersupply_power_efficiency_percentage": {"1|1|Power Supply Bay 1": 94},
		// PSU2 reports no readings, so no (misleading) zero values are exported for it.
		"redfish_chassis_power_powersupply_power_input_watts":       {"1|1|Power Supply Bay 1": 533},
		"redfish_chassis_power_powersupply_power_output_watts":      {"1|1|Power Supply Bay 1": 513},
		"redfish_chassis_power_powersupply_last_power_output_watts": {"1|1|Power Supply Bay 1": 513},
	}, got)
}

func TestChassisCollectorPowerSuppliesFromLegacyPower(t *testing.T) {
	srv := newRedfishMock(t, map[string]string{
		"/redfish/v1":         mockServiceRoot,
		"/redfish/v1/Chassis": mockChassisCollection,
		"/redfish/v1/Chassis/NVMeSSD.0.Group.0.StorageBackplane": mockBackplaneChassis,
		"/redfish/v1/Chassis/1":                                  mockChassisLegacyPower,
		"/redfish/v1/Chassis/1/Power":                            mockLegacyPower,
		"/redfish/v1/Chassis/1/Thermal":                          mockLegacyThermal,
	})

	got := gatherPowerSupplyMetrics(t, srv)

	assert.Equal(t, map[string]map[string]float64{
		"redfish_chassis_power_powersupply_health":                      {"1|0|Power Supply Bay 1": 1, "1|1|Power Supply Bay 2": 3},
		"redfish_chassis_power_powersupply_state":                       {"1|0|Power Supply Bay 1": 1, "1|1|Power Supply Bay 2": 1},
		"redfish_chassis_power_powersupply_power_capacity_watts":        {"1|0|Power Supply Bay 1": 1200, "1|1|Power Supply Bay 2": 1200},
		"redfish_chassis_power_powersupply_power_efficiency_percentage": {"1|0|Power Supply Bay 1": 94, "1|1|Power Supply Bay 2": 0},
		"redfish_chassis_power_powersupply_power_input_watts":           {"1|0|Power Supply Bay 1": 231, "1|1|Power Supply Bay 2": 0},
		"redfish_chassis_power_powersupply_power_output_watts":          {"1|0|Power Supply Bay 1": 216, "1|1|Power Supply Bay 2": 0},
		"redfish_chassis_power_powersupply_last_power_output_watts":     {"1|0|Power Supply Bay 1": 210, "1|1|Power Supply Bay 2": 0},
	}, got)
}
