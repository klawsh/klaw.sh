# klaw Helm chart

Deploys the klaw controller and worker nodes on any conformant Kubernetes cluster
(EKS, GKE, AKS, OpenShift, k3s, kind, on-prem). Nothing in the chart depends on a cloud provider.

| Component | Kind | Notes |
|---|---|---|
| controller | StatefulSet (1 replica) + Service `:9090` | File-backed state on a PVC. The etcd store is not implemented upstream, so it cannot be scaled out. |
| node | Deployment (default) or DaemonSet | Outbound gRPC only, no Service. Optional HPA and PDB for the Deployment. |

## Install

```bash
helm install klaw ./deploy/helm/klaw \
  --namespace klaw --create-namespace \
  --set image.repository=<your-registry>/klaw \
  --set anthropic.existingSecret=klaw-anthropic   # key: ANTHROPIC_API_KEY
```

No image is published yet. Build one from the `Containerfile` at the repo root and push it to a registry your cluster can pull from.

## Common settings

| Value | Purpose |
|---|---|
| `auth.existingSecret` / `auth.token` | Shared node token (secret key `token`). A random token is generated and kept across upgrades if neither is set. |
| `anthropic.existingSecret` / `anthropic.apiKey` | Required when `node.enabled=true`. |
| `agents[]` | Agents seeded into every node: `name`, `description`, `model`, `systemPrompt`, `tools`, `skills`, `triggers`. |
| `cluster.name`, `cluster.namespace` | klaw cluster/namespace the nodes serve (not the Kubernetes ones). |
| `controller.persistence.*` | `storageClass` (empty = cluster default, `-` = none), `existingClaim`, `size`. |
| `node.kind`, `node.replicas`, `node.autoscaling.*` | Workload shape. |
| `serviceAccount.annotations` | Cloud identity: AWS IRSA, GKE Workload Identity, Azure Workload Identity. |
| `nodeSelector`, `tolerations`, `affinity`, `topologySpreadConstraints` | Per component under `controller.*` and `node.*`. |
| `imagePullSecrets` | Private registries. |

### Workload identity examples

```yaml
serviceAccount:
  annotations:
    eks.amazonaws.com/role-arn: arn:aws:iam::123456789012:role/klaw     # EKS
    # iam.gke.io/gcp-service-account: klaw@PROJECT.iam.gserviceaccount.com   # GKE
    # azure.workload.identity/client-id: 00000000-0000-0000-0000-000000000000  # AKS
```

Note that klaw itself does not call cloud APIs today, so this only matters for custom tools or extra env you add.

### OpenShift

Let the SCC choose the UID:

```bash
--set podSecurityContext.runAsUser=null,podSecurityContext.runAsGroup=null,podSecurityContext.fsGroup=null
```

## Security defaults

Pods satisfy the Pod Security Standard `restricted`: non-root (UID 65532), read-only root filesystem,
all capabilities dropped, `RuntimeDefault` seccomp, no service account token mounted.

## Known upstream limitations

Discovered while testing this chart; they are not fixed here.

- **Nodes do not reconnect after a controller restart.** `internal/node/grpc_client.go` has no reconnect logic, so after the controller pod restarts, node pods stay `Running` but disconnected until they are restarted (`kubectl rollout restart deploy/<release>-node`).
- `klaw node start` has no `--labels` flag (only `node join` does), so node labels cannot be set from the chart.
- `klaw node start` leaves the node name empty unless `--name` is given; the chart passes the pod name.
- Controller/node traffic is unencrypted gRPC (`insecure.NewCredentials()`).

## Testing

```bash
for f in deploy/helm/klaw/ci/*-values.yaml; do
  helm lint deploy/helm/klaw -f "$f"
  helm template r deploy/helm/klaw -f "$f" | kubeconform -strict -kubernetes-version 1.30.0
done
```

Verified on kind v1.35 in a namespace enforcing the `restricted` Pod Security Standard: controller and
nodes start, agents register, controller state survives a pod restart, a wrong token is rejected.
Not verified: OpenShift, GKE, AKS, EKS.
