import { Container } from '@cloudflare/containers';
export default { async fetch(request, env) { return env.MY_CONTAINER.get(env.MY_CONTAINER.idFromName("singleton")).fetch(request); } }
export class MyContainer extends Container { defaultPort = 8080; }
