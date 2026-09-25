# Seccomp profile recording

Generates seccomp profiles for the `service-a`/`service-b` workloads in both
`tls-demo-full` and `tls-demo-stripped` by recording their real syscalls with
the [Security Profiles Operator](https://github.com/kubernetes-sigs/security-profiles-operator)
(SPO), then restricting them to only those syscalls. Standard defensive
hardening: record real behavior first, then lock it down.

All of this is wired into `task seccomp:*` — see `.taskfiles/Seccomp.yml`
for the task definitions and `seccomp/recordings/` for the `ProfileRecording`
manifests (Task 2).

## Order of operations

```sh
task seccomp:install-operator     # cert-manager + SPO, one-time per cluster
task seccomp:enable-recording      # label both namespaces for SPO
task seccomp:start-recording       # apply ProfileRecordings, restart the pods

# ... let it run and exercise the app: normal traffic, error paths,
#     startup, and any periodic/background work ...

task seccomp:stop-recording         # finalizes recording -> SeccompProfile CRDs
task seccomp:list                   # see what was produced
task seccomp:export                 # dump every profile to ./seccomp-profiles/
task seccomp:diff                   # compare full vs stripped syscall surfaces
```

`task seccomp:show NAME=service-a NS=tls-demo-full` prints one profile's full
JSON for closer inspection; `task seccomp:show-all` prints all 4 at once (no
args needed).

`task seccomp:scale-down` / `task seccomp:scale-up` scale service-a/service-b
to 0 and back to 1 in both namespaces. `stop-recording` already calls these
itself (see the troubleshooting section below for why), but they're also
useful standalone for manually recovering a recording stuck on the lock
finalizer.

## Two caveats before you do anything with these profiles

1. **A profile only contains syscalls seen *during* recording.** Rare code
   paths that didn't run while SPO was watching — a retry branch, a signal
   handler, a once-a-day cron path — won't be in the profile. If you later
   enforce it, those paths get blocked and can break the app in ways that
   won't show up until they happen in production. Record for long enough,
   under realistic conditions, before trusting the output.

2. **Audit before you enforce.** Before applying a recorded profile,
   `SeccompProfile.spec.defaultAction` should be `SCMP_ACT_LOG` first —
   let it run in audit mode and watch for violations in the node logs. Only
   switch to `SCMP_ACT_ERRNO` (actually blocking disallowed syscalls) once
   those logs are clean.

   Actually **applying** a profile to the pods — setting
   `securityContext.seccompProfile.type: Localhost` and `localhostProfile`
   on the `tls-demo` chart's Deployments — is deliberately **not** part of
   these tasks. That's a separate, deliberate step you take after reviewing
   the recorded output, not something to automate blindly.

## Troubleshooting: `seccomp:list` stays empty

Three distinct, confirmed causes — found by reading SPO v1.1.0's own source
(`internal/pkg/manager/recordingtracker/recordingtracker.go` and
`internal/pkg/daemon/bpfrecorder/`) after debugging a stuck cluster live.
Check all three, since more than one can apply at once:

1. **Is `spod`'s BPF recorder actually enabled?** `operator.yaml` does not
   turn this on — `kubectl get spod spod -n security-profiles-operator -o
   yaml` must show `spec.enricher.enableBpfRecorder: true`, and the daemon
   container's own args (`kubectl get pod <spod-pod> -n
   security-profiles-operator -o jsonpath='{.spec.containers[0].args}'`)
   should read `--with-recording=true`. If it's `false`, `recorder: Bpf` in
   a `ProfileRecording` silently does nothing: the controller still
   annotates pods for recording, but spod itself never runs a recorder, so
   its lock finalizer waits forever for a session that never starts, and
   spod's own logs stay completely silent (no "Starting BPF recorder on
   node" line) no matter how long you wait. `install-operator` patches this
   on automatically now; a cluster set up before that fix needs it applied
   by hand once — `task seccomp:install-operator` is idempotent and safe to
   re-run for exactly this.

2. **`stop-recording`'s finalizer only clears once every traced POD is
   actually deleted — not when the `ProfileRecording` is deleted.** A
   `RecordingTrackerReconciler` inside SPO's manager watches Pods, adds
   every pod matching a recording's `podSelector` to that recording's
   `status.activeWorkloads` (even if the recording is already mid-deletion),
   and only drops the lock finalizer once that list is empty. Since
   service-a/service-b are long-running Deployments that never exit on
   their own, the finalizer can hang forever with the pods just sitting
   there healthy — that's expected, not a hang to wait out. `kubectl
   rollout restart` does not fix it either: Kubernetes' default
   RollingUpdate creates the new pod *before* deleting the old one, so a
   still-existing `ProfileRecording` immediately re-tracks the replacement,
   and the cycle never ends. This is why `stop-recording` scales the
   deployments to 0 (releasing the tracked pod with nothing left to
   re-track it) and back to 1 afterward, instead of just deleting the
   `ProfileRecording` and waiting — see `seccomp:scale-down`/`scale-up` if
   you need to do this by hand (e.g. a stuck `ProfileRecording` still shows
   a `deletionTimestamp` that never clears in `kubectl get profilerecording
   -A -o yaml`).

3. **`SeccompProfile` is cluster-scoped, not namespaced** (`kubectl
   api-resources | grep seccompprofile` shows `NAMESPACED: false`) — so a
   plain `kubectl get seccompprofile -n <ns>` silently ignores the `-n` and
   either lists everything or (before any profile exists) just looks empty
   either way, which can look identical to namespace-scoping actually
   working. `list`/`show`/`show-all`/`export`/`clean` all filter on the
   `spo.x-k8s.io/recording-namespace` label instead, which is what's
   actually set correctly on every generated profile.
