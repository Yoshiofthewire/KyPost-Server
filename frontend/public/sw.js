self.addEventListener("push", (event) => {
  let payload = {};
  if (event.data) {
    try {
      payload = event.data.json();
    } catch {
      payload = { body: event.data.text() };
    }
  }

  const title = typeof payload.title === "string" && payload.title.trim() ? payload.title : "Llama Mail";
  const body = typeof payload.body === "string" ? payload.body : "You have a new notification.";
  const url = typeof payload.url === "string" && payload.url.trim() ? payload.url : "/notifications";
  const tag = typeof payload.tag === "string" && payload.tag.trim() ? payload.tag : undefined;

  event.waitUntil(
    self.registration.showNotification(title, {
      body,
      tag,
      data: { url },
      badge: "/pwa-icon.svg",
      icon: "/pwa-icon.svg"
    })
  );
});

self.addEventListener("notificationclick", (event) => {
  event.notification.close();
  const url = (event.notification && event.notification.data && event.notification.data.url) || "/notifications";

  event.waitUntil(
    clients.matchAll({ type: "window", includeUncontrolled: true }).then((clientList) => {
      for (const client of clientList) {
        if ("focus" in client) {
          client.navigate(url);
          return client.focus();
        }
      }
      if (clients.openWindow) {
        return clients.openWindow(url);
      }
      return undefined;
    })
  );
});
