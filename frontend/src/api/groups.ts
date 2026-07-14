import { getJSON, postJSON, putJSON, deleteJSON } from "./client";

export type Group = {
  id: string;
  name: string;
  rev: number;
  createdAt: string;
  updatedAt: string;
};

type GroupsListResponse = {
  groups: Group[];
};

export async function listGroups(): Promise<Group[]> {
  const res = await getJSON<GroupsListResponse>("/api/groups");
  return res.groups ?? [];
}

export function createGroup(name: string): Promise<Group> {
  return postJSON<Group>("/api/groups", { name });
}

export function renameGroup(id: string, name: string): Promise<Group> {
  return putJSON<Group>(`/api/groups/${encodeURIComponent(id)}`, { name });
}

export function deleteGroup(id: string): Promise<{ ok: boolean; removed: boolean }> {
  return deleteJSON<{ ok: boolean; removed: boolean }>(`/api/groups/${encodeURIComponent(id)}`);
}
