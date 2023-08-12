export default {
    async fetch(request, env, ctx) {
        let value = await env.KV.get("foo");
        await env.KV.put("foo", value + "r");

        let obj = await env.R2.get("foo");
        if (!obj) {
            obj = "ba";
        } else {
            obj = await obj.text();
        }
        await env.R2.put("foo", obj + "r");

        return new Response(`${value} / ${obj}`);
    },
};
