import { expect, test as base } from '@playwright/test'

/**
 * Every test fails if the browser reports an error: uncaught exceptions,
 * console errors, and Content-Security-Policy violations. A too-strict CSP
 * would otherwise break the app silently (a blocked script or style is only
 * a console message).
 */
export const test = base.extend<{ browserErrors: string[] }>({
  browserErrors: [
    async ({ context }, use) => {
      const errors: string[] = []
      const watch = (page: import('@playwright/test').Page) => {
        page.on('pageerror', (err) => errors.push(`pageerror: ${err.message}`))
        page.on('console', (msg) => {
          if (msg.type() === 'error') errors.push(`console: ${msg.text()}`)
        })
      }
      context.pages().forEach(watch)
      context.on('page', watch)
      await use(errors)
      // 401 from /api/me is the normal "not signed in" answer.
      const real = errors.filter((e) => !e.includes('status of 401'))
      expect(real, 'browser errors').toEqual([])
    },
    { auto: true },
  ],
})

export { expect }
