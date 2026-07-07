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
    placeholder: "podman run -d --name jellyfin -p 8096:8096 -v /srv/media:/media:Z docker.io/jellyfin/jellyfin:latest",
  },
  compose: {
    label: "compose file",
    help: "Paste a Docker Compose or podman-compose YAML file. Services become container units.",
    placeholder: "services:\n  app:\n    image: ...",
  },
  container: {
    label: "running container",
    help: "Import an existing container configuration as a Quadlet unit. The running container is not changed.",
    placeholder: "",
  },
} as const;
