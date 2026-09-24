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
JSON for closer inspection.

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
