export interface Binding {
  key: string;
  kind: "path" | "runtime" | "resource" | "secret";
  localLocator?: string;
  secretRef?: string;
}

export interface Command {
  executable: { kind: string; relativePath?: string; binding?: string; requirementId?: string; tool?: string };
  args: string[];
  cwd: { binding: string; relativePath: string };
  envBindings: Record<string, string>;
  timeoutSeconds?: number;
}

export interface Unit {
  label: string;
  kind: string;
  control: string;
  resourceId: string;
  dependsOn: string[];
  requirementIds: string[];
  start?: Command;
  stop?: { kind: string; command?: Command; timeoutSeconds?: number; reason?: string };
  readiness?: { kind: string; timeoutSeconds: number };
}

export interface ProjectDocument {
  schemaVersion: string;
  projectId: string;
  name: string;
  group?: string;
  source: { kind: string; identity: string };
  requirements: Record<string, { component: string; range: string }>;
  files: Record<string, unknown>;
  appearance?: { coverFileId?: string };
  units: Record<string, Unit>;
  profiles: Record<string, { label: string; unitIds: string[]; featureIds: string[] }>;
}

export interface ProjectView {
  document: ProjectDocument;
  revision: number;
  runSummary: string;
  checkSummary: string;
}

export interface ProfileRef {
  projectId: string;
  profileId: string;
  expectedRevision: number;
}

export interface CheckReport {
  summary: string;
  items: {
    requirementId: string;
    status: string;
    evidence: { version?: string; source: string }[];
    reasons: { code: string; message: string; subject: string }[];
    repairs: { id: string; label: string; effect: string; automaticSupported: boolean }[];
  }[];
}

export interface ContainerView {
  id: string;
  name: string;
  image: string;
  status: string;
  composeProject: string;
  composeService: string;
  relationship: string;
  control: string;
  shared: boolean;
  projectIds: string[] | null;
}
