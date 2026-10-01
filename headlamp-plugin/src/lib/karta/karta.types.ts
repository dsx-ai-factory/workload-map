// SPDX-License-Identifier: Apache-2.0
// Copyright (c) 2026 NVIDIA Corporation

export interface Karta {
  apiVersion: string;
  kind: string;
  // resourceVersion is absent on catalog definitions, which never change.
  metadata: { name: string; resourceVersion?: string };
  // Optional at every level because the CRD requires none of it: a Karta
  // applied while the validating webhook is down can be missing spec
  // entirely. Note this only documents the shape, it does not enforce it --
  // the plugin's tsconfig leaves strictNullChecks off, so readers get the
  // warning but the compiler will not.
  spec?: {
    structureDefinition?: {
      rootComponent?: {
        kind?: GroupVersionKind;
      };
      // Counted, never read: not a full mirror of ComponentDefinition.
      childComponents?: { name: string }[];
    };
  };
}

export interface Workload {
  apiVersion: string;
  kind: string;
  metadata: {
    name: string;
    namespace?: string;
    creationTimestamp?: string;
    uid?: string;
    resourceVersion?: string;
  };
  [field: string]: unknown;
}

export interface GroupVersionKind {
  group: string;
  version: string;
  kind: string;
}

export interface Scale {
  replicas?: number;
  minReplicas?: number;
  maxReplicas?: number;
}

export interface WorkloadStatus {
  Phases: string[];
}

export interface ComponentNode {
  Name: string;
  Kind: GroupVersionKind | null;
  HasPodDefinition: boolean;
  Instances: InstanceNode[];
}

export interface InstanceNode {
  InstanceKey: string | null;
  ReplicaKey: string | null;
  Scale: Scale | null;
  ExtractedInstance: unknown;
  Children: ComponentNode[];
}

export interface WorkloadTree {
  Status: WorkloadStatus | null;
  Children: ComponentNode[];
}
