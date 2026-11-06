import { expect, type Page } from '@playwright/test'

/**
 * Signs in through the real flow: app → /auth/login → dev OIDC provider
 * ("Sign in as …") → /auth/callback → back in the app.
 */
export async function signIn(page: Page, name: string, startAt = '/') {
  await page.goto(startAt)
  await page.getByRole('link', { name: 'Sign in with Google' }).click()
  await expect(page.getByRole('heading', { name: 'Dev sign-in' })).toBeVisible()
  await page.getByRole('button', { name: `Sign in as ${name}` }).click()
}

/** Creates the user's family from the first-run screen. */
export async function createFamily(page: Page, name: string) {
  await page.getByLabel('Family name').fill(name)
  await page.getByRole('button', { name: 'Create family' }).click()
  await expect(page.getByRole('grid')).toBeVisible()
}

/** Today as YYYY-MM-DD in the browser's local time. */
export function todayKey(): string {
  const d = new Date()
  const pad = (n: number) => String(n).padStart(2, '0')
  return `${String(d.getFullYear())}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
}
