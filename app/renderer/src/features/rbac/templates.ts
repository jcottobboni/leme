// Starting points for "new resource". NAMESPACE is replaced with the selected namespace.
export interface Template {
  id: string
  group: string
  label: string
  yaml: string
}

export const TEMPLATES: Template[] = [
  {
    id: 'blank',
    group: 'Geral',
    label: 'YAML em branco',
    yaml: `apiVersion: v1
kind: ConfigMap
metadata:
  name: my-config
  namespace: NAMESPACE
data:
  key: value
`,
  },
  {
    id: 'role',
    group: 'RBAC',
    label: 'Role (namespace)',
    yaml: `apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: pod-reader
  namespace: NAMESPACE
rules:
  - apiGroups: [""]
    resources: ["pods", "pods/log"]
    verbs: ["get", "list", "watch"]
`,
  },
  {
    id: 'rolebinding',
    group: 'RBAC',
    label: 'RoleBinding',
    yaml: `apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: pod-reader-binding
  namespace: NAMESPACE
subjects:
  - kind: User
    name: someone@example.com
    apiGroup: rbac.authorization.k8s.io
  # - kind: ServiceAccount
  #   name: my-app
  #   namespace: NAMESPACE
  # - kind: Group
  #   name: my-team
  #   apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: Role
  name: pod-reader
  apiGroup: rbac.authorization.k8s.io
`,
  },
  {
    id: 'clusterrole',
    group: 'RBAC',
    label: 'ClusterRole',
    yaml: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: namespace-viewer
rules:
  - apiGroups: [""]
    resources: ["namespaces"]
    verbs: ["get", "list", "watch"]
`,
  },
  {
    id: 'clusterrolebinding',
    group: 'RBAC',
    label: 'ClusterRoleBinding',
    yaml: `apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: namespace-viewer-binding
subjects:
  - kind: Group
    name: my-team
    apiGroup: rbac.authorization.k8s.io
roleRef:
  kind: ClusterRole
  name: namespace-viewer
  apiGroup: rbac.authorization.k8s.io
`,
  },
  {
    id: 'serviceaccount',
    group: 'RBAC',
    label: 'ServiceAccount',
    yaml: `apiVersion: v1
kind: ServiceAccount
metadata:
  name: my-app
  namespace: NAMESPACE
`,
  },
  {
    id: 'deployment',
    group: 'Workloads',
    label: 'Deployment',
    yaml: `apiVersion: apps/v1
kind: Deployment
metadata:
  name: my-app
  namespace: NAMESPACE
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
          image: nginx:1.27-alpine
          ports:
            - containerPort: 80
`,
  },
  {
    id: 'service',
    group: 'Network',
    label: 'Service',
    yaml: `apiVersion: v1
kind: Service
metadata:
  name: my-app
  namespace: NAMESPACE
spec:
  selector:
    app: my-app
  ports:
    - port: 80
      targetPort: 80
`,
  },
  {
    id: 'secret',
    group: 'Config',
    label: 'Secret',
    yaml: `apiVersion: v1
kind: Secret
metadata:
  name: my-secret
  namespace: NAMESPACE
type: Opaque
stringData:
  password: change-me
`,
  },
]

export function templateYAML(id: string, namespace: string) {
  const t = TEMPLATES.find((x) => x.id === id) ?? TEMPLATES[0]
  return t.yaml.replaceAll('NAMESPACE', namespace || 'default')
}
