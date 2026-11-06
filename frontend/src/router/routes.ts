import { useSyncExternalStore } from "react";

export function projectHref(id: string, tab = "overview") {
  return `#/projects/${encodeURIComponent(id)}/${tab}`;
}

export function containerHref(id: string) {
  return `#/docker/${encodeURIComponent(id)}`;
}

function subscribe(listener: () => void) {
  window.addEventListener("hashchange", listener);
  return () => window.removeEventListener("hashchange", listener);
}

function location() {
  return window.location.hash || "#/projects";
}

export function useRoute() {
  const [section, rawID, tab] = useSyncExternalStore(subscribe, location).slice(2).split("/");
  let id: string | undefined;
  try {
    id = rawID ? decodeURIComponent(rawID) : undefined;
  } catch {
    // Invalid deep links return to the section landing page.
  }
  return { section, id, tab };
}
