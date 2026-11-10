# go.dokimi.dev/sim

## 0.12.0

### Minor Changes

- b5e4f07: **Breaking:** Prove each convergence of a simulation on the system `Convergence.Defect` in `sim.Falsify` and `Simulation.Prove`.

### Patch Changes

- b5e4f07: Fail `Simulation.Prove` for a name that two checks of the simulation share.
- b5e4f07: Fail `Simulation.Prove` with a message that contains the check for an invariant or a convergence without a planted defect.

## 0.11.0

### Minor Changes

- 837c2be: Complete a call of a component through `World.Invoke` as unknown for an error that `Anatomy.Unknown` reports.

### Patch Changes

- e23304b: Fix `sim.Run` to judge the rules of a runtime over each entry of the log once, in O(n) time for a case of n steps.
- Updated dependencies [859f112]
  - go.dokimi.dev/conform@0.17.0

## 0.10.2

### Patch Changes

- be74e71: Fix `sim.Run` to start a host that exits in the final phase again, at once and then after a delay that doubles from 10 s to 5 minutes.

## 0.10.1

### Patch Changes

- 83af074: Fix `World.LoseAfter` to apply the stop, the message and the launch of the outcome of the effect that it loses.

## 0.10.0

### Minor Changes

- 2873955: Give a case of `sim.Test`, `Simulation.Prove` and `sim.Fuzz` 82 choices for each step of `Simulation.MaxSteps`.
- 2873955: **Breaking:** Add the action `deliver fast` to `sim.Run`, at the weight of the other actions of the active phase in the half of the cases whose swarm keeps it.
- 2873955: **Breaking:** Keep the actions `deliver` and `advance` in every case of `sim.Run`.
- 4028586: Add `World.AfterFunc` to call a function after a duration on the clock of a process and then poll each wait.

### Patch Changes

- 207b7f0: Require `go.dokimi.dev/assert` v0.20.0.
- 4028586: Fix `sim.Run` to give the final phase 1,000 steps for each second of the longest `Within` and at least 10,000 steps.
- Updated dependencies [207b7f0]
  - go.dokimi.dev/conform@0.16.3
  - go.dokimi.dev/fault@0.2.4
  - go.dokimi.dev/host@0.2.1
  - go.dokimi.dev/medium@0.2.1
  - go.dokimi.dev/spec@0.5.1

## 0.9.0

### Minor Changes

- 8c0c0c3: Add `Clock.Monotonic` for the time since the start of a host at the drift of its clock.

## 0.8.0

### Minor Changes

- b7033ce: Add `Simulation.MeanSteps` and `Simulation.MaxSteps` for the mean and the most steps of the active phase of a case.
- b7033ce: **Breaking:** End each process of the host of a machine that stops with an error, and mark the host as exited.
- b7033ce: Restart an exited host through `World.Restart`, `World.Recover` and the action `restart`.

### Patch Changes

- Updated dependencies [933642e]
  - go.dokimi.dev/conform@0.16.2

## 0.7.0

### Minor Changes

- f228fb9: Add `Anatomy.End` for the event that ends a wait at an outage after the wait has delivered an event.
- f228fb9: Add `Anatomy.Ends` and cancel the wait of the token that it returns for an effect.
- f228fb9: Add `Outcome.Cancel` and call it when the world drops the outcome of an effect or ends its wait.

## 0.6.0

### Minor Changes

- 820257d: Add `sim.Host`, `World.AddHost` and `Host.Spawn` to run machine processes on one host, whose faults strike each of them.
- 820257d: Add `Outcome.Launch` and `Outcome.Stop` to start and stop a machine on the host of the process of an effect.
- 820257d: Add `World.Now` to return the time of the run, which no drift and no fault of a host moves.
- 820257d: Change `World.Outage` to end each wait of the dependency with the event of the failure of its effect.
- 820257d: **Breaking:** Change `sim.Outcome` to take the type arguments `[E, F]`, so `sim.Perform` returns `sim.Outcome[E, F]`.
- 820257d: **Breaking:** Change `sim.System` to take the case, so a system takes its inputs from the case before the first step.

### Patch Changes

- d14d1ac: Fix `sim.Run` to check the history after each step without an open call.
- 820257d: Fix `sim.Run` to check the rules against each step of a machine before the world performs the effects of the step.
- 820257d: Fix `sim.Test` and `Simulation.Prove` to run each scenario on the simplest case alone, without the edge cases of the property engine.
- Updated dependencies [41eb868]
  - go.dokimi.dev/conform@0.16.1

## 0.5.0

### Minor Changes

- c2e9099: **Breaking:** Change a run with a component to choose the client of a call, the target of a fault of the datagrams and the byte of a corruption among all clients and processes.

### Patch Changes

- Updated dependencies [ae3dfa7, 6131ab9]
  - go.dokimi.dev/host@0.2.0
  - go.dokimi.dev/conform@0.16.0

## 0.4.1

### Patch Changes

- c315378: Fix the checks of the whole history and of a scenario to end a case whose history the checker cannot decide without a failure, under the label `undecided`.
- Updated dependencies [84491b7]
  - go.dokimi.dev/conform@0.15.0

## 0.4.0

### Minor Changes

- c240592: Add `sim.Component.Running` to report whether an incarnation of a component process runs.

## 0.3.0

### Minor Changes

- 2034159: Add `sim.ComponentOf` to return the component process of a world by its name.

## 0.2.0

### Minor Changes

- e9bca72: Add `sim.SpawnComponent`, `sim.Component` and `World.Invoke` to run Go components on simulated hosts beside the machine processes.
- e9bca72: Add the fault `disk-replacement` and `World.ReplaceDisk` to crash a component until a restart over a new empty disk.
- e9bca72: Check the history of the calls of the clients after every step of `sim.Run`, so a violation ends its case at its step.
- e9bca72: Add `Simulation.History` for the options of each check of the history against the spec.
- e9bca72: Add `Simulation.Around`, `Simulation.Mode`, `Simulation.Network` and `Simulation.Blocking` for the hosts of the components of a simulation.
- e9bca72: Add the faults `reset`, `slow-link` and `restore-link` of the network of the components, with `World.Reset`, `World.SlowLink` and `World.RestoreLink`.
- e9bca72: Add `sim.None`, the type of the events and the effects of a world without machine processes.

### Patch Changes

- 1cbb662: Require `go.dokimi.dev/assert` v0.18.0.
- Updated dependencies [984587a, e7c7771, 531155e, 2831344, 1cbb662, e49374f]
  - go.dokimi.dev/conform@0.14.0
  - go.dokimi.dev/medium@0.2.0
  - go.dokimi.dev/host@0.1.0
  - go.dokimi.dev/fault@0.2.3
  - go.dokimi.dev/spec@0.5.0

## 0.1.3

### Patch Changes

- a43530a: Require `go.dokimi.dev/assert` v0.17.0.
- Updated dependencies [a43530a]
  - go.dokimi.dev/medium@0.1.1
  - go.dokimi.dev/spec@0.4.1

## 0.1.2

### Patch Changes

- Updated dependencies [45feafe]
  - go.dokimi.dev/spec@0.4.0

## 0.1.1

### Patch Changes

- Updated dependencies [b9681ea]
  - go.dokimi.dev/spec@0.3.0

## 0.1.0

### Minor Changes

- c327410: Add the module `go.dokimi.dev/sim`, the runtime of the deterministic simulation of a system of sans-IO machines.
- 65922e1: Crash each `medium.Memory` among the dependencies of a process with the process.
- 65922e1: Add the faults `torn-write`, `corruption` and `full-disk` of the media of a process.

### Patch Changes

- Updated dependencies [1bc3e13, 317d5ff, 0a9d402, d2d7f23]
  - go.dokimi.dev/medium@0.1.0
  - go.dokimi.dev/spec@0.2.0
