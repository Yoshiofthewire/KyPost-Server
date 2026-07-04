import { getJSON, postJSON, putJSON } from "./client";
import type { Role } from "../auth";

export type ManagedUser = {
  id: string;
  username: string;
  role: Role;
  active: boolean;
  mustChangePassword: boolean;
  createdAt: string;
  updatedAt: string;
  deactivatedAt?: string;
};

type UsersListResponse = {
  users: ManagedUser[];
};

export async function listUsers(): Promise<ManagedUser[]> {
  const res = await getJSON<UsersListResponse>("/api/users");
  return res.users ?? [];
}

export function createUser(username: string, password: string, role: Role): Promise<ManagedUser> {
  return postJSON<ManagedUser>("/api/users", { username, password, role });
}

export function setUserRole(id: string, role: Role): Promise<ManagedUser> {
  return putJSON<ManagedUser>(`/api/users/${encodeURIComponent(id)}`, { role });
}

export function resetUserPassword(id: string, password: string): Promise<ManagedUser> {
  return postJSON<ManagedUser>(`/api/users/${encodeURIComponent(id)}/reset-password`, { password });
}

export function deactivateUser(id: string): Promise<ManagedUser> {
  return postJSON<ManagedUser>(`/api/users/${encodeURIComponent(id)}/deactivate`, {});
}

export function reactivateUser(id: string): Promise<ManagedUser> {
  return postJSON<ManagedUser>(`/api/users/${encodeURIComponent(id)}/reactivate`, {});
}
