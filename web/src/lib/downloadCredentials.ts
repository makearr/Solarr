/** qBittorrent signs in with a user name and a password; Solarr keeps them as one sealed secret, "user:password"
 *  (the first colon separates, so the password may contain one). Both empty is a host that lets Solarr in unasked. */
export function joinCredentials(username: string, password: string): string {
  if (username === "" && password === "") return "";
  return `${username.trim()}:${password}`;
}
