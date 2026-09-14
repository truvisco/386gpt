#!/usr/bin/env node

const [hostname, sshHost] = process.argv.slice(2)
if (!hostname || !sshHost) {
  console.error('usage: ensure-api-dns.mjs HOSTNAME SSH_HOST')
  process.exit(2)
}

const token = process.env.CLOUDFLARE_API_TOKEN
const accountId = process.env.CLOUDFLARE_ACCOUNT_ID
if (!token || !accountId) {
  console.error('CLOUDFLARE_API_TOKEN and CLOUDFLARE_ACCOUNT_ID are required')
  process.exit(1)
}

const { execFileSync } = await import('node:child_process')
const address = execFileSync(
  'ssh',
  ['-o', 'BatchMode=yes', sshHost, 'curl -fsS --max-time 10 https://api.ipify.org'],
  { encoding: 'utf8' },
).trim()

if (!/^\d{1,3}(\.\d{1,3}){3}$/.test(address)) {
  throw new Error(`web host returned an invalid IPv4 address: ${address}`)
}

const headers = {
  Authorization: `Bearer ${token}`,
  'Content-Type': 'application/json',
}

async function cloudflare(path, options = {}) {
  const response = await fetch(`https://api.cloudflare.com/client/v4${path}`, {
    ...options,
    headers: { ...headers, ...options.headers },
  })
  const body = await response.json()
  if (!response.ok || !body.success) {
    const details = [...(body.errors ?? []), ...(body.messages ?? [])]
      .map((item) => item.message)
      .filter(Boolean)
      .join('; ')
    throw new Error(`Cloudflare request failed (${response.status}): ${details || 'unknown error'}`)
  }
  return body.result
}

const zoneName = hostname.split('.').slice(-2).join('.')
const zones = await cloudflare(`/zones?name=${encodeURIComponent(zoneName)}&account.id=${encodeURIComponent(accountId)}`)
if (zones.length !== 1) {
  throw new Error(`expected one Cloudflare zone for ${zoneName}, received ${zones.length}`)
}

const zoneId = zones[0].id
const records = await cloudflare(`/zones/${zoneId}/dns_records?type=A&name=${encodeURIComponent(hostname)}`)
const desired = { type: 'A', name: hostname, content: address, ttl: 1, proxied: true }

if (records.length === 0) {
  await cloudflare(`/zones/${zoneId}/dns_records`, {
    method: 'POST',
    body: JSON.stringify(desired),
  })
  console.log(`Created proxied DNS record ${hostname}.`)
} else {
  await cloudflare(`/zones/${zoneId}/dns_records/${records[0].id}`, {
    method: 'PUT',
    body: JSON.stringify(desired),
  })
  console.log(`Updated proxied DNS record ${hostname}.`)
}
