import { apiVersionOf } from "./k8s";
import type { ResourceDef } from "./resources";

const ns = (namespace: string) => namespace || "default";

export const templates: Record<string, (namespace: string) => string> = {
  Deployment: (n) => `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: ${ns(n)}
  labels:
    app: my-app
spec:
  replicas: 1
  selector:
    matchLabels:
      app: my-app
  template:
    metadata:
      labels:
        app: my-app
    spec:
      containers:
        - name: app
          image: nginx:1.27
          ports:
            - containerPort: 80
          resources:
            requests:
              cpu: 50m
              memory: 64Mi
`,
  Service: (n) => `apiVersion: v1
kind: Service
metadata:
  name: my-app
  namespace: ${ns(n)}
spec:
  type: ClusterIP
  selector:
    app: my-app
  ports:
    - port: 80
      targetPort: 80
`,
  Pod: (n) => `apiVersion: v1
kind: Pod
metadata:
  name: debug
  namespace: ${ns(n)}
spec:
  containers:
    - name: debug
      image: busybox:1.36
      command: ["sleep", "3600"]
  restartPolicy: Never
`,
  ConfigMap: (n) => `apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: ${ns(n)}
data:
  key: value
`,
  Secret: (n) => `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: ${ns(n)}
type: Opaque
stringData:
  password: change-me
`,
  Job: (n) => `apiVersion: batch/v1
kind: Job
metadata:
  name: my-job
  namespace: ${ns(n)}
spec:
  backoffLimit: 2
  template:
    spec:
      restartPolicy: Never
      containers:
        - name: job
          image: busybox:1.36
          command: ["sh", "-c", "echo hello"]
`,
  CronJob: (n) => `apiVersion: batch/v1
kind: CronJob
metadata:
  name: my-cronjob
  namespace: ${ns(n)}
spec:
  schedule: "*/15 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: job
              image: busybox:1.36
              command: ["sh", "-c", "date"]
`,
  Namespace: () => `apiVersion: v1
kind: Namespace
metadata:
  name: my-namespace
`,
  Ingress: (n) => `apiVersion: networking.k8s.io/v1
kind: Ingress
metadata:
  name: my-app
  namespace: ${ns(n)}
spec:
  rules:
    - host: my-app.example.com
      http:
        paths:
          - path: /
            pathType: Prefix
            backend:
              service:
                name: my-app
                port:
                  number: 80
`,
  PersistentVolumeClaim: (n) => `apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: my-data
  namespace: ${ns(n)}
spec:
  accessModes: ["ReadWriteOnce"]
  resources:
    requests:
      storage: 1Gi
`,
};

export function newResourceTemplate(def: ResourceDef, namespace: string): string {
  const t = templates[def.kind];
  if (t) return t(namespace);
  return `apiVersion: ${apiVersionOf(def.gvr)}
kind: ${def.kind}
metadata:
  name: my-${def.kind.toLowerCase()}
${def.namespaced ? `  namespace: ${ns(namespace)}\n` : ""}`;
}
