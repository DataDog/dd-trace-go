core.register_action("send_blocking_response", { "http-req" }, function(txn)
    print("send_blocking_response")

    local body = txn:get_var("txn.dd.body")
    local status_code = txn:get_var("txn.dd.status_code")
    local headers = txn:get_var("txn.dd.headers")

    local reply = txn:reply()
    reply:set_status(status_code)

    local LINE_ITER = "[^\r\n]+"
    local LINE_KV_STRICT = "^([%w%-]+): (%S.+)$"
    for line in headers:gmatch(LINE_ITER) do
        local k, v = line:match(LINE_KV_STRICT)
        if k then
            reply:add_header(k, v)
        end
    end
    
    reply:set_body(body)
    txn:done(reply)
end)
