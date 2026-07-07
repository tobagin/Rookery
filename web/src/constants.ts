import type { AuthState } from "./lib";

export const defaultAuth: AuthState = {
  required: false,
  authenticated: true,
  readOnly: false,
  setupNeeded: false,
  onboarding: false,
  username: "",
  email: "",
  role: "",
  oidc: null,
  passwordLogin: true,
};

export const TEMPLATES: Record<string, string> = {
  container: `[Unit]
Description=My container

[Container]
Image=docker.io/library/nginx:latest
PublishPort=8080:80
# Volume=/srv/data:/data:Z

[Service]
Restart=always

[Install]
WantedBy=default.target
`,
  pod: `[Pod]
PublishPort=8080:80
`,
  network: `[Network]
Subnet=10.89.0.0/24
`,
  volume: `[Volume]
`,
  kube: `[Kube]
Yaml=deployment.yml
`,
  image: `[Image]
Image=docker.io/library/nginx:latest
`,
  build: `[Build]
ImageTag=localhost/myimage:latest
File=Containerfile
`,
};

export const IMPORT_MODES = {
  run: {
    label: "podman run command",
    help: "Paste a podman run or docker run command; multi-line commands with backslashes are supported.",
    placeholder: "podman run -d --name nginx-edge -p 8080:8080 docker.io/nginxinc/nginx-unprivileged:1.27-alpine",
  },
  compose: {
    label: "compose file",
    help: "Paste a Docker Compose or podman-compose YAML file. Services become container units.",
    placeholder: `services:
  edge:
    image: docker.io/nginxinc/nginx-unprivileged:1.27-alpine
    ports:
      - "8080:8080"
  cache:
    image: docker.io/library/redis:7-alpine
    volumes:
      - cache-data:/data

volumes:
  cache-data:
`,
  },
  container: {
    label: "running container",
    help: "Import an existing container configuration as a Quadlet unit. The running container is not changed.",
    placeholder: "",
  },
} as const;
