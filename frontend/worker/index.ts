export default {
  fetch() {
		return new Response('Not found', { status: 404 })
  },
} satisfies ExportedHandler<Env>
