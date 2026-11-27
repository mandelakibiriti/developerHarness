// Data entering the app. Right now it is trusted blindly.
export type User = { id: string; email: string; age: number };

export function parseUser(body: string): User {
  const data: any = JSON.parse(body);
  return data;
}
