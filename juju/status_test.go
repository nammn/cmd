package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	. "launchpad.net/gocheck"
	"launchpad.net/goyaml"
	"launchpad.net/juju-core/charm"
	"launchpad.net/juju-core/cmd"
	"launchpad.net/juju-core/juju"
	"launchpad.net/juju-core/juju/testing"
	"launchpad.net/juju-core/state"
	"launchpad.net/juju-core/state/api/params"
	"launchpad.net/juju-core/state/presence"
	coretesting "launchpad.net/juju-core/testing"
	"launchpad.net/juju-core/version"
	"net/url"
	"time"
)

func runStatus(c *C, args ...string) (code int, stdout, stderr []byte) {
	ctx := coretesting.Context(c)
	code = cmd.Main(&StatusCommand{}, ctx, args)
	stdout = ctx.Stdout.(*bytes.Buffer).Bytes()
	stderr = ctx.Stderr.(*bytes.Buffer).Bytes()
	return
}

type StatusSuite struct {
	testing.JujuConnSuite
}

var _ = Suite(&StatusSuite{})

type M map[string]interface{}

type testCase struct {
	summary string
	steps   []stepper
}

func test(summary string, steps ...stepper) testCase {
	return testCase{summary, steps}
}

type stepper interface {
	step(c *C, ctx *context)
}

type context struct {
	st      *state.State
	conn    *juju.Conn
	charms  map[string]*state.Charm
	pingers map[string]*presence.Pinger
}

func (s *StatusSuite) newContext() *context {
	return &context{
		st:      s.State,
		conn:    s.Conn,
		charms:  make(map[string]*state.Charm),
		pingers: make(map[string]*presence.Pinger),
	}
}

func (s *StatusSuite) resetContext(c *C, ctx *context) {
	for _, up := range ctx.pingers {
		err := up.Kill()
		c.Check(err, IsNil)
	}
	s.JujuConnSuite.Reset(c)
}

func (ctx *context) run(c *C, steps []stepper) {
	for i, s := range steps {
		c.Logf("step %d", i)
		c.Logf("%#v", s)
		s.step(c, ctx)
	}
}

// shortcuts for expected output.
var (
	machine0 = M{
		"agent-state": "started",
		"dns-name":    "dummyenv-0.dns",
		"instance-id": "dummyenv-0",
	}
	machine1 = M{
		"agent-state": "started",
		"dns-name":    "dummyenv-1.dns",
		"instance-id": "dummyenv-1",
	}
	machine2 = M{
		"agent-state": "started",
		"dns-name":    "dummyenv-2.dns",
		"instance-id": "dummyenv-2",
	}
	unexposedService = M{
		"charm":   "local:series/dummy-1",
		"exposed": false,
	}
	exposedService = M{
		"charm":   "local:series/dummy-1",
		"exposed": true,
	}
)

type outputFormat struct {
	name      string
	marshal   func(v interface{}) ([]byte, error)
	unmarshal func(data []byte, v interface{}) error
}

// statusFormats list all output formats supported by status command.
var statusFormats = []outputFormat{
	{"yaml", goyaml.Marshal, goyaml.Unmarshal},
	{"json", json.Marshal, json.Unmarshal},
}

var statusTests = []testCase{
	test(
		"bootstrap and starting a single instance",

		// unlikely, as you can't run juju status in real life without
		// machine/0 bootstrapped.
		expect{
			"empty state",
			M{
				"machines": M{},
				"services": M{},
			},
		},

		addMachine{"0", state.JobManageEnviron},
		expect{
			"simulate juju bootstrap by adding machine/0 to the state",
			M{
				"machines": M{
					"0": M{
						"instance-id": "pending",
					},
				},
				"services": M{},
			},
		},

		startAliveMachine{"0"},
		expect{
			"simulate the PA starting an instance in response to the state change",
			M{
				"machines": M{
					"0": M{
						"agent-state": "pending",
						"dns-name":    "dummyenv-0.dns",
						"instance-id": "dummyenv-0",
					},
				},
				"services": M{},
			},
		},

		setMachineStatus{"0", params.MachineStarted, ""},
		expect{
			"simulate the MA started and set the machine status",
			M{
				"machines": M{
					"0": machine0,
				},
				"services": M{},
			},
		},

		setTools{"0", &state.Tools{
			Binary: version.Binary{
				Number: version.MustParse("1.2.3"),
				Series: "gutsy",
				Arch:   "ppc",
			},
			URL: "http://canonical.com/",
		}},
		expect{
			"simulate the MA setting the version",
			M{
				"machines": M{
					"0": M{
						"dns-name":      "dummyenv-0.dns",
						"instance-id":   "dummyenv-0",
						"agent-version": "1.2.3",
						"agent-state":   "started",
					},
				},
				"services": M{},
			},
		},
	), test(
		"add two services and expose one, then add 2 more machines and some units",
		addMachine{"0", state.JobManageEnviron},
		startAliveMachine{"0"},
		setMachineStatus{"0", params.MachineStarted, ""},
		addCharm{"dummy"},
		addService{"dummy-service", "dummy"},
		addService{"exposed-service", "dummy"},
		expect{
			"no services exposed yet",
			M{
				"machines": M{
					"0": machine0,
				},
				"services": M{
					"dummy-service":   unexposedService,
					"exposed-service": unexposedService,
				},
			},
		},

		setServiceExposed{"exposed-service", true},
		expect{
			"one exposed service",
			M{
				"machines": M{
					"0": machine0,
				},
				"services": M{
					"dummy-service":   unexposedService,
					"exposed-service": exposedService,
				},
			},
		},

		addMachine{"1", state.JobHostUnits},
		startAliveMachine{"1"},
		setMachineStatus{"1", params.MachineStarted, ""},
		addMachine{"2", state.JobHostUnits},
		startAliveMachine{"2"},
		setMachineStatus{"2", params.MachineStarted, ""},
		expect{
			"two more machines added",
			M{
				"machines": M{
					"0": machine0,
					"1": machine1,
					"2": machine2,
				},
				"services": M{
					"dummy-service":   unexposedService,
					"exposed-service": exposedService,
				},
			},
		},

		addUnit{"dummy-service", "1"},
		addAliveUnit{"exposed-service", "2"},
		setUnitStatus{"exposed-service/0", params.UnitError, "You Require More Vespene Gas"},
		// Simulate some status with no info, while the agent is down.
		setUnitStatus{"dummy-service/0", params.UnitStarted, ""},
		expect{
			"add two units, one alive (in error state), one down",
			M{
				"machines": M{
					"0": machine0,
					"1": machine1,
					"2": machine2,
				},
				"services": M{
					"exposed-service": M{
						"charm":   "local:series/dummy-1",
						"exposed": true,
						"units": M{
							"exposed-service/0": M{
								"machine":          "2",
								"agent-state":      "error",
								"agent-state-info": "You Require More Vespene Gas",
							},
						},
					},
					"dummy-service": M{
						"charm":   "local:series/dummy-1",
						"exposed": false,
						"units": M{
							"dummy-service/0": M{
								"machine":     "1",
								"agent-state": "down",
							"agent-state-info": "started",
							},
						},
					},
				},
			},
		},

		addMachine{"3", state.JobHostUnits},
		startMachine{"3"},
		// Simulate some status with info, while the agent is down.
		setMachineStatus{"3", params.MachineStopped, "Really?"},
		addMachine{"4", state.JobHostUnits},
		startAliveMachine{"4"},
		setMachineStatus{"4", params.MachineError, "Beware the red toys"},
		expect{
			"add two more machine, one with a dead agent, one in error state",
			M{
				"machines": M{
					"0": machine0,
					"1": machine1,
					"2": machine2,
					"3": M{
						"dns-name":         "dummyenv-3.dns",
						"instance-id":      "dummyenv-3",
						"agent-state":      "down",
						"agent-state-info": "stopped: Really?",
					},
					"4": M{
						"dns-name":         "dummyenv-4.dns",
						"instance-id":      "dummyenv-4",
						"agent-state":      "error",
						"agent-state-info": "Beware the red toys",
					},
				},
				"services": M{
					"exposed-service": M{
						"charm":   "local:series/dummy-1",
						"exposed": true,
						"units": M{
							"exposed-service/0": M{
								"machine":          "2",
								"agent-state":      "error",
								"agent-state-info": "You Require More Vespene Gas",
							},
						},
					},
					"dummy-service": M{
						"charm":   "local:series/dummy-1",
						"exposed": false,
						"units": M{
							"dummy-service/0": M{
								"machine":          "1",
								"agent-state":      "down",
								"agent-state-info": "started",
							},
						},
					},
				},
			},
		},
	),
}

// TODO(dfc) test failing components by destructively mutating the state under the hood

type addMachine struct {
	machineId string
	job       state.MachineJob
}

func (am addMachine) step(c *C, ctx *context) {
	m, err := ctx.st.AddMachine("series", am.job)
	c.Assert(err, IsNil)
	c.Assert(m.Id(), Equals, am.machineId)
}

type startMachine struct {
	machineId string
}

func (sm startMachine) step(c *C, ctx *context) {
	m, err := ctx.st.Machine(sm.machineId)
	c.Assert(err, IsNil)
	inst := testing.StartInstance(c, ctx.conn.Environ, m.Id())
	err = m.SetProvisioned(inst.Id(), "fake_nonce")
	c.Assert(err, IsNil)
}

type startAliveMachine struct {
	machineId string
}

func (sam startAliveMachine) step(c *C, ctx *context) {
	m, err := ctx.st.Machine(sam.machineId)
	c.Assert(err, IsNil)
	pinger, err := m.SetAgentAlive()
	c.Assert(err, IsNil)
	ctx.st.StartSync()
	err = m.WaitAgentAlive(200 * time.Millisecond)
	c.Assert(err, IsNil)
	agentAlive, err := m.AgentAlive()
	c.Assert(err, IsNil)
	c.Assert(agentAlive, Equals, true)
	inst := testing.StartInstance(c, ctx.conn.Environ, m.Id())
	err = m.SetProvisioned(inst.Id(), "fake_nonce")
	c.Assert(err, IsNil)
	ctx.pingers[m.Id()] = pinger
}

type setTools struct {
	machineId string
	tools     *state.Tools
}

func (st setTools) step(c *C, ctx *context) {
	m, err := ctx.st.Machine(st.machineId)
	c.Assert(err, IsNil)
	err = m.SetAgentTools(st.tools)
	c.Assert(err, IsNil)
}

type addCharm struct {
	name string
}

func (ac addCharm) step(c *C, ctx *context) {
	ch := coretesting.Charms.Dir(ac.name)
	name, rev := ch.Meta().Name, ch.Revision()
	curl := charm.MustParseURL(fmt.Sprintf("local:series/%s-%d", name, rev))
	bundleURL, err := url.Parse(fmt.Sprintf("http://bundles.example.com/%s-%d", name, rev))
	c.Assert(err, IsNil)
	dummy, err := ctx.st.AddCharm(ch, curl, bundleURL, fmt.Sprintf("%s-%d-sha256", name, rev))
	c.Assert(err, IsNil)
	ctx.charms[ac.name] = dummy
}

type addService struct {
	name  string
	charm string
}

func (as addService) step(c *C, ctx *context) {
	ch, ok := ctx.charms[as.charm]
	c.Assert(ok, Equals, true)
	_, err := ctx.st.AddService(as.name, ch)
	c.Assert(err, IsNil)
}

type setServiceExposed struct {
	name    string
	exposed bool
}

func (sse setServiceExposed) step(c *C, ctx *context) {
	s, err := ctx.st.Service(sse.name)
	c.Assert(err, IsNil)
	if sse.exposed {
		err = s.SetExposed()
		c.Assert(err, IsNil)
	}
}

type addUnit struct {
	serviceName string
	machineId   string
}

func (au addUnit) step(c *C, ctx *context) {
	s, err := ctx.st.Service(au.serviceName)
	c.Assert(err, IsNil)
	u, err := s.AddUnit()
	c.Assert(err, IsNil)
	m, err := ctx.st.Machine(au.machineId)
	c.Assert(err, IsNil)
	err = u.AssignToMachine(m)
	c.Assert(err, IsNil)
}

type addAliveUnit struct {
	serviceName string
	machineId   string
}

func (aau addAliveUnit) step(c *C, ctx *context) {
	s, err := ctx.st.Service(aau.serviceName)
	c.Assert(err, IsNil)
	u, err := s.AddUnit()
	c.Assert(err, IsNil)
	pinger, err := u.SetAgentAlive()
	c.Assert(err, IsNil)
	ctx.st.StartSync()
	err = u.WaitAgentAlive(200 * time.Millisecond)
	c.Assert(err, IsNil)
	agentAlive, err := u.AgentAlive()
	c.Assert(err, IsNil)
	c.Assert(agentAlive, Equals, true)
	m, err := ctx.st.Machine(aau.machineId)
	c.Assert(err, IsNil)
	err = u.AssignToMachine(m)
	c.Assert(err, IsNil)
	ctx.pingers[u.Name()] = pinger
}

type setUnitStatus struct {
	unitName   string
	status     params.UnitStatus
	statusInfo string
}

func (sus setUnitStatus) step(c *C, ctx *context) {
	u, err := ctx.st.Unit(sus.unitName)
	err = u.SetStatus(sus.status, sus.statusInfo)
	c.Assert(err, IsNil)
}

type setMachineStatus struct {
	machineId  string
	status     params.MachineStatus
	statusInfo string
}

func (sms setMachineStatus) step(c *C, ctx *context) {
	m, err := ctx.st.Machine(sms.machineId)
	err = m.SetStatus(sms.status, sms.statusInfo)
	c.Assert(err, IsNil)
}

type expect struct {
	what   string
	output M
}

func (e expect) step(c *C, ctx *context) {
	c.Log("expect: %s", e.what)

	// Now execute the command for each format.
	for _, format := range statusFormats {
		c.Logf("format %q", format.name)
		// Run command with the required format.
		code, stdout, stderr := runStatus(c, "--format", format.name)
		c.Assert(code, Equals, 0)
		c.Assert(stderr, HasLen, 0)

		// Prepare the output in the same format.
		buf, err := format.marshal(e.output)
		c.Assert(err, IsNil)
		expected := make(M)
		err = format.unmarshal(buf, &expected)
		c.Assert(err, IsNil)

		// Check the output is as expected.
		actual := make(M)
		err = format.unmarshal(stdout, &actual)
		c.Assert(err, IsNil)
		c.Assert(actual, DeepEquals, expected)
	}
}

func (s *StatusSuite) TestStatusAllFormats(c *C) {
	for i, t := range statusTests {
		c.Log("test %d: %s", i, t.summary)
		func() {
			// Prepare context and run all steps to setup.
			ctx := s.newContext()
			defer s.resetContext(c, ctx)
			ctx.run(c, t.steps)
		}()
	}
}
