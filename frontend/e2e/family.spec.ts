import { expect, test } from './fixtures'

import { createFamily, signIn } from './helpers'

test('an invited relative joins and shares an event', async ({ browser, browserErrors }) => {
  // Two people, two separate browsers (separate cookies).
  const bobCtx = await browser.newContext()
  const carolCtx = await browser.newContext()
  const bob = await bobCtx.newPage()
  const carol = await carolCtx.newPage()
  for (const p of [bob, carol]) {
    p.on('pageerror', (err) => browserErrors.push(`pageerror: ${err.message}`))
    p.on('console', (msg) => {
      if (msg.type() === 'error') browserErrors.push(`console: ${msg.text()}`)
    })
  }

  // Bob creates a family and an invite link.
  await signIn(bob, 'Bob Smith')
  await createFamily(bob, 'Bob’s family')
  await bob.getByRole('link', { name: 'Family', exact: true }).click()
  await bob.getByRole('button', { name: 'Create invite link' }).click()
  const inviteURL = await bob.getByLabel('Invite link').inputValue()
  expect(inviteURL).toMatch(/\/invite#[A-Za-z0-9_-]{43}$/)
  await bob.screenshot({ path: 'test-results/screens/04-family-invite.png', fullPage: true })

  // Carol opens the link while signed out, signs in, and joins.
  await carol.goto(inviteURL)
  await expect(carol.getByRole('heading', { name: "You're invited" })).toBeVisible()
  expect(new URL(carol.url()).hash).toBe('') // token removed from the address bar
  await carol.getByRole('link', { name: 'Sign in with Google' }).click()
  await carol.getByRole('button', { name: 'Sign in as Carol Jones' }).click()
  await expect(carol.getByRole('heading', { name: 'Join a family' })).toBeVisible()
  await carol.getByRole('button', { name: 'Join family' }).click()
  await expect(carol.getByRole('link', { name: 'Bob’s family' })).toBeVisible()

  // The link worked once.
  await bob.reload()
  await expect(bob.getByText('Carol Jones')).toBeVisible()

  // Bob plans an event and adds Carol.
  await bob.getByRole('link', { name: 'Calendar' }).click()
  await bob.getByRole('link', { name: 'New event' }).click()
  await bob.getByLabel('Title').fill('Zoo trip')
  await bob.getByLabel('All day').check()
  await bob.getByRole('checkbox', { name: 'Carol Jones' }).check()
  await bob.getByRole('button', { name: 'Create event' }).click()
  await expect(bob.getByRole('link', { name: /Zoo trip/ })).toBeVisible()

  // Carol sees it, with both participants.
  await carol.reload()
  const row = carol
    .getByRole('region', { name: /Events on/ })
    .getByRole('link', { name: /Zoo trip/ })
  await expect(row).toContainText('All day')
  await expect(row.getByLabel('2 participants')).toBeVisible()
  await carol.screenshot({ path: 'test-results/screens/05-shared-event.png', fullPage: true })

  // Phone layout.
  await carol.setViewportSize({ width: 390, height: 844 })
  await carol.screenshot({ path: 'test-results/screens/06-phone.png', fullPage: true })

  await bobCtx.close()
  await carolCtx.close()
})
