// 登录用的人机验证：服务端发一道题，浏览器找一个数字 nonce，
// 让 sha256(题目 + ":" + nonce) 的前 difficulty 位都是 0。平均要算 2^difficulty 次。

import { post } from './api'

const enc = new TextEncoder()

function leadingZeroBits(buf: ArrayBuffer): number {
  let n = 0
  for (const b of new Uint8Array(buf)) {
    if (b !== 0) return n + Math.clz32(b) - 24
    n += 8
  }
  return n
}

export async function solveCaptcha(): Promise<{ captcha_token: string; captcha_nonce: string }> {
  const { token, difficulty } = await post<{ token: string; difficulty: number }>('/api/auth/challenge')
  const batch = 500 // 一批并发算，比逐个 await 快很多，也不会卡住页面
  for (let start = 0; ; start += batch) {
    const sums = await Promise.all(
      Array.from({ length: batch }, (_, i) => crypto.subtle.digest('SHA-256', enc.encode(`${token}:${start + i}`))),
    )
    const hit = sums.findIndex((s) => leadingZeroBits(s) >= difficulty)
    if (hit >= 0) return { captcha_token: token, captcha_nonce: String(start + hit) }
  }
}
