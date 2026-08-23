export function navigateToProvider(url: string, location: Pick<Location, "assign"> = window.location): void {
  const target = new URL(url, window.location.origin);
  if (target.origin !== window.location.origin) {
    throw new Error("provider navigation must begin on the Proctor origin");
  }
  location.assign(target.href);
}
