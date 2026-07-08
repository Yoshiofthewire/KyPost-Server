import { getJSON, postJSON, putJSON, deleteJSON } from "./client";

export type ContactValue = {
  label?: string;
  value: string;
};

export type ContactAddress = {
  label?: string;
  street?: string;
  city?: string;
  region?: string;
  postalCode?: string;
  country?: string;
};

export type Contact = {
  uid: string;
  rev: number;
  deleted?: boolean;
  createdAt: string;
  updatedAt: string;
  fn: string;
  givenName?: string;
  familyName?: string;
  middleName?: string;
  prefix?: string;
  suffix?: string;
  nickname?: string;
  org?: string;
  title?: string;
  emails?: ContactValue[];
  phones?: ContactValue[];
  addresses?: ContactAddress[];
  notes?: string;
  birthday?: string;
};

export type ContactInput = {
  fn: string;
  givenName?: string;
  familyName?: string;
  middleName?: string;
  prefix?: string;
  suffix?: string;
  nickname?: string;
  org?: string;
  title?: string;
  emails?: ContactValue[];
  phones?: ContactValue[];
  addresses?: ContactAddress[];
  notes?: string;
  birthday?: string;
};

type ContactsListResponse = {
  contacts: Contact[];
};

export async function listContacts(): Promise<Contact[]> {
  const res = await getJSON<ContactsListResponse>("/api/contacts");
  return res.contacts ?? [];
}

export function createContact(input: ContactInput): Promise<Contact> {
  return postJSON<Contact>("/api/contacts", input);
}

export function updateContact(uid: string, input: ContactInput): Promise<Contact> {
  return putJSON<Contact>(`/api/contacts/${encodeURIComponent(uid)}`, input);
}

export function deleteContact(uid: string): Promise<{ ok: boolean; removed: boolean }> {
  return deleteJSON<{ ok: boolean; removed: boolean }>(`/api/contacts/${encodeURIComponent(uid)}`);
}

export type DAVPasswordStatus = {
  configured: boolean;
  createdAt?: string;
};

export function getDAVPasswordStatus(): Promise<DAVPasswordStatus> {
  return getJSON<DAVPasswordStatus>("/api/contacts/dav-password");
}

export type DAVPasswordGenerated = {
  password: string;
  createdAt: string;
};

export function generateDAVPassword(): Promise<DAVPasswordGenerated> {
  return postJSON<DAVPasswordGenerated>("/api/contacts/dav-password", {});
}

export function revokeDAVPassword(): Promise<{ ok: boolean }> {
  return deleteJSON<{ ok: boolean }>("/api/contacts/dav-password");
}
