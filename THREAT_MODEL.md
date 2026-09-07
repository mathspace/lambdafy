# lambdafy threat model

## Overview

Operator CLI and container runtime adapter for ordinary HTTP services on AWS Lambda. CLI wraps a Docker image with /lambdafy-proxy, pushes ECR content, publishes versioned Lambda configurations, and deploys public active/preactive aliases with SQS and Scheduler triggers. Proxy translates Lambda HTTP/SQS/cron events to a child HTTP application; outside Lambda it directly execs the child after environment dereferencing (make.go:20, publish.go:165, deploy.go:241, proxy/main.go:56, proxy/main.go:97).

| Component or workflow | Source |
| --- | --- |
| Image/publish/deploy CLI | make.go:35, publish.go:165, deploy.go:241 |
| HTTP/trigger adapter | proxy/main.go:56 |
| Local queue broker | proxy/sqs.go:144 |
| Release/admin | delete.go:45, .github/workflows/release.yaml:3 |

Effective resources below describe configured consumers, including separately authorized operator workflows. Credential values are represented only by references.

| Deployment or workflow | Resource or capability | Configuration and precedence | Safe effective value or location | Readers, writers, or recipients | Enforcing control | Evidence or unknowns |
| --- | --- | --- | --- | --- | --- | --- |
| Lambda | child HTTP | random base19000..19999 -> PORT override -> child launch | 127.0.0.1:&lt;PORT&gt;; /_lambdafy/sqs and /_lambdafy/cron internal handlers | proxy and child within same container | public handler rejects reserved raw prefix; redirects disabled | proxy/main.go:46, proxy/main.go:149, proxy/http.go:24 |
| non-Lambda | child launch | starenv dereference -> exec.LookPath -> syscall.Exec | original command under same OS environment; no local broker started | operator-selected application | OS account, inherited credentials | proxy/main.go:114, proxy/main.go:126 |
| Lambda | queue-send broker | starenv ARN -> ID map -> local /sqs -> AWS SDK | 127.0.0.1:&lt;PORT+1&gt;/sqs?id=&lt;generated&gt;; target SQS regional ARN-derived URL | child/local processes; configured queue | POST and ID-map checks; AWS IAM | proxy/sqs.go:31, proxy/sqs.go:119, proxy/sqs.go:144 |
| publish generate | function role | default policy + extra -> content-derived IAM role | lambdafy-v1-&lt;policy hash&gt;; Lambda+Scheduler trust | runtime child/proxy and Scheduler | AWS IAM; default broad policy plus supplied statements | publish.go:32, publish.go:74, publish.go:269 |
| deploy | public service | version -> preactive/active aliases -> Function URL config | public AWS Function URLs, AuthType NONE | Internet callers | application auth external; configured CORS; AWS URL permission | deploy.go:141, deploy.go:168, deploy.go:278 |
| publish/deploy | logs | spec retention -> stored metadata -> CloudWatch retention | /aws/lambda/&lt;function&gt;, default90d | runtime writers and IAM-authorized readers | CloudWatch IAM and retention setting | fnspec/fnspec.go:21, log_retention.go:20, deploy.go:265 |
| configured publish | EFS/VPC | spec EFSMounts/subnet/SG -> Lambda configuration | operator-specified access point/local mount and VPC resources | function runtime | AWS access-point/IAM/network controls external | fnspec/fnspec.go:95, publish.go:328 |

## Threat Model, Trust Boundaries, and Assumptions

Protected assets: AWS operator credentials and IAM role/pass-role authority; ECR images, function versions/aliases, schedule/queue bindings, EFS data, function environment and dereferenced secrets, CloudWatch logs. Child HTTP application data including any student PII; internal cron/SQS handlers, queue message integrity and processing availability. Lambda function role is shared runtime authority, not sandboxed away from child process.

Security objectives: Keep public HTTP separate from internal trigger routes and application authorization; preserve secrets/PII through env, logs, responses and exports. Restrict Lambda role, queue destinations, EFS mounts and VPC access to intended resources; make queue retries/idempotency and deployment partial-state recovery explicit. Bind published code/version and active alias to intended AWS account/region; preserve operator control over destructive administrative operations and release supply chain.

Actors and initial capabilities: Unauthenticated users can invoke deployed public HTTP URLs but lack AWS deploy permissions; queue producers control message data within externally granted SQS rights. Malicious HTTP/queue payload may cross parsing/internal-route boundaries into application authority. Child app already shares function credentials, process/container and environment; random ports/IDs do not create a separate AWS principal. Spec/image/release maintainers can intentionally change code and cloud roles. A caller granting publish authority must trust their spec and local Docker input; account glob checks are guardrails, not an authorization system.

- Operator YAML/stdin and substitutions → spec parser → AWS mutations: KnownFields validates YAML, required fields and account/region glob checks constrain publish when configured. Empty allowed-account list allows any configured account. AWS SDK default chain selects credentials/region; STS caller identity is checked before publish (fnspec/fnspec.go:108, fnspec/fnspec.go:133, publish.go:199).
- Operator Docker daemon → modified local image → ECR: make inspects original entrypoint, adds embedded proxy and preserves child invocation; push authenticates via ECR, uses image digest as tag, and can create repository. Docker daemon and input image are privileged operator inputs, not runtime user uploads (make.go:35, push.go:44, push.go:111).
- Publish/deploy → Lambda/IAM/network/storage: supplied role name/ARN or generated content-derived role; default policy grants EC2 networking/logging/SQS and lambda:InvokeFunction on *. Lambda and Scheduler trust that generated role. EFS access point/local mount, VPC subnets/SGs, memory, timeout and ephemeral storage come from spec; AWS IAM/access points/SGs enforce actual privileges (publish.go:32, publish.go:74, publish.go:269, publish.go:328, fnspec/fnspec.go:79).
- Internet → function URL → proxy → child app: deploy creates AuthType NONE and public invoke-URL permission for preactive and active aliases; this is not user authentication. CORS is configuration, not server-side authorization. HTTP event raw /_lambdafy/ paths are rejected before forwarding; body/base64 and headers become local HTTP and responses are encoded/compressed, with redirects disabled in proxy client (deploy.go:141, deploy.go:168, proxy/http.go:20, proxy/main.go:36).
- SQS event and Scheduler → internal child paths: batch records concurrently POST body to /_lambdafy/sqs; cron POSTs escaped name to /_lambdafy/cron. Child 2xx/3xx is success; failures influence Lambda batch retry behavior. IAM invocation/event-source permissions, message producers and application idempotency govern these paths, not the public URL authentication (proxy/sqs.go:40, proxy/cron.go:10, publish.go:437).
- Child application → local SQS broker: starenv lambdafy_sqs_send ARN reference produces a random-ID URL at 127.0.0.1:(PORT+1)/sqs?id=&lt;id&gt;, mapped to https://sqs.&lt;region&gt;.amazonaws.com/&lt;account&gt;/&lt;queue&gt;. Broker requires POST and mapped ID, then sends using default AWS credentials. This narrows broker destinations but does not remove child access to inherited AWS/environment authority (proxy/sqs.go:31, proxy/sqs.go:114, proxy/sqs.go:144, proxy/main.go:114).
- Release operator → public rollout: preactive public URL is primed for non-5xx, then new-version SQS triggers enabled before old-version disable, cron schedule group recreated, active alias switched. This is a multistep operational transition, not transactional deployment; temporary dual queue consumption and failed partial transitions require operator recovery (deploy.go:278, deploy.go:306, deploy.go:328).
- Administrative CLI paths include alias updates/removal, function/schedule deletion, generated-role cleanup, log/spec retrieval; exported spec/environment and logs are sensitive operator outputs. Tag-triggered CI publishes CLI releases; setup.sh downloads/extracts release executable into ~/bin (alias.go:48, delete.go:45, delete.go:77, spec.go:49, logs.go:70, .github/workflows/release.yaml:3, setup.sh:4).

Assumptions and unresolved controls:

- User-provided context: most services run on AWS and almost all sit behind Cloudflare; that does not establish an origin restriction. Student minors’ PII is extremely sensitive wherever it enters these flows.
- Cloudflare is general supplied context; function URL creation here is publicly invokable without a local Cloudflare-only origin restriction. App-level auth and per-service data contracts are external.
- Runtime has no independent child sandbox: all LAMBDAFY_ metadata is removed, then starenv dereferences environment, child inherits remaining environment and AWS authority. Outside Lambda syscall.Exec does not start the HTTP/SQS broker although dereferencing still occurs (proxy/main.go:103, proxy/main.go:126).
- Runtime app endpoint is random 127.0.0.1:19000–19999, broker next port; PORT is overridden only in Lambda (proxy/main.go:46, proxy/main.go:149).
- Log retention defaults to90 days and deploy enforces /aws/lambda/&lt;function&gt; through configured spec metadata; external collectors/exports have separate retention (fnspec/fnspec.go:21, log_retention.go:20, deploy.go:265).
- Source inventory contains proxy source/build script but not generated embedded binary; release workflow invokes GoReleaser without a committed configuration in inventory. Build/release artifacts and live IAM policies were not examined.

## Attack Surface, Mitigations, and Attacker Stories

These are prioritized hypotheses for future validation, not vulnerability findings. Priority reflects potential impact subject to the listed prerequisites. Existing controls may reject a scenario; absent deployment evidence is not proof of failure.

| Priority | Scenario and capability gain | Prerequisites | Impact | Existing controls | Mitigation | Evidence |
| --- | --- | --- | --- | --- | --- | --- |
| High | Public HTTP reaches an internal SQS/cron handler or unauthorized application data. | Path normalization/routing mismatch or absent application-level authorization. | Privileged task invocation or student-data access. | Raw /_lambdafy/ prefix rejection; distinct event translation; default client does not follow redirects. | Verify effective downstream path semantics and authenticate application operations independently of CORS. | proxy/http.go:20, proxy/sqs.go:66, proxy/cron.go:10, deploy.go:141 |
| High | Untrusted application input gains Lambda role, EFS or queue authority. | Defect in hosted application or parser exposes runtime execution/credentials. | Data disclosure, queue mutation or lateral AWS capability. | AWS IAM, configured VPC/EFS access; broker queue-ID allow map. | Scope function role and mounts; treat child and proxy as one credential principal. | publish.go:32, publish.go:328, proxy/main.go:114, proxy/sqs.go:144 |
| High | An operator deploys a spec/version to unintended account or role. | Wrong default AWS context, omitted guardrail or untrusted spec substitutions. | Wrong-service replacement or excessive runtime permissions. | KnownFields parsing; optional account/region glob check before publish. | Require explicit target policy and verify resolved identity/spec before mutations; restrict pass-role. | fnspec/fnspec.go:108, fnspec/fnspec.go:133, publish.go:199 |
| High | Partial rollout duplicates queue work or leaves stale trigger authority. | Failure during multistep alias/SQS/schedule transition; non-idempotent work. | Data corruption or material service interruption. | Preactive priming; new consumers enabled before old disabled; versioned functions. | Require idempotent work and inspect/reconcile exact trigger/alias state after failure. | deploy.go:278, deploy.go:306, deploy.go:328 |
| Medium | Queue payloads or excessive broker output exhaust runtime resources. | Producer has queue permissions or compromised app uses local broker. | Delayed/repeated processing and additional AWS cost. | Configured batch sizes/memory/timeout; batch send max10; per-record failure reporting. | Set workload-specific limits and test retry semantics; bound payload and processing concurrency. | proxy/sqs.go:27, proxy/sqs.go:56, proxy/sqs.go:97, fnspec/fnspec.go:91 |
| High | Secret-bearing environment/spec/log output reaches unauthorized reader. | Overbroad operator read/export/log access or app logs sensitive payload. | Credential or PII disclosure. | CloudWatch IAM/retention; internal LAMBDAFY_ metadata stripped before child. | Control export/log recipients; retain reference-based secrets and avoid sensitive diagnostics. | spec.go:49, logs.go:70, log_retention.go:20, proxy/main.go:103 |
| Conditional High | Release/image/admin path substitutes code or deletes service resources. | Compromised privileged maintainer/local Docker/release path; not ordinary request access. | Function compromise or outage. | Operator AWS/Docker authorization; versioned release and ECR digest tag. | Verify artifacts, target/version and narrowly scope release/delete roles; retain recoverable versions. | make.go:35, push.go:143, delete.go:45, .github/workflows/release.yaml:3, setup.sh:4 |

## Severity Calibration (Critical, High, Medium, Low)

- **Critical:** Critical requires demonstrated mass sensitive-data compromise or broad AWS control gained by a lower-trust caller. Public function URLs and intended child code execution do not alone meet that threshold.
- **High:** High fits public-to-internal operation bypass, unauthorized role/resource access, destructive wrong-account deployment, or corruption from a failed rollout. App authorization and actual role grants determine impact.
- **Medium:** Medium fits bounded queue amplification, expensive retries or recoverable rollout stalls. Lack of a broker ID is not a durable security boundary against a child already holding AWS credentials.
- **Low:** Low fits non-sensitive logging or metadata drift without privilege gain. Random ports and queue IDs discourage coupling; they do not isolate child and proxy principals.

This model is an offline architecture review, not completed audit coverage. It does not validate live credentials, network exposure, external modules, service permissions or runtime artifacts.

Repository: https://github.com/mathspace/lambdafy
Version: `32068350f9928f9ad2ed8df19380debf19e68bd8`
