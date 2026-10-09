// 64 characters divide the byte range exactly, avoiding modulo bias.
const alphabet =
  "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789_-";

export function generatePassword() {
  if (!globalThis.crypto?.getRandomValues) {
    throw new Error("浏览器不支持安全随机数，请手动设置密码");
  }
  let password;
  do {
    const bytes = globalThis.crypto.getRandomValues(new Uint8Array(16));
    password = Array.from(bytes, (byte) => alphabet[byte & 63]).join("");
  } while (
    !/[A-Z]/.test(password) ||
    !/[a-z]/.test(password) ||
    !/[0-9]/.test(password) ||
    !/[_-]/.test(password)
  );
  return password;
}
