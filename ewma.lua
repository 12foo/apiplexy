-- This is a lua file for testing the EWMA code that is used
-- in apiplexy. It fakes redis for that purpose.

local redis = {}
local storage = {}

function redis.call(...)
    local arg = {...}
    if arg[1] == 'GET' then
        if storage[arg[2]] then
            return storage[arg[2]]
        else
            return false
        end
    elseif arg[1] == 'SETEX' then
        storage[arg[2]] = tostring(arg[4])
    end
end

-- the content of this function is the code that will be loaded into
-- redis when apiplexy starts up.
local function do_ewma(KEYS, ARGV)
    local kts, kavg = unpack(KEYS)
    local now, max, period, cost = tonumber(ARGV[1]), tonumber(ARGV[2]), tonumber(ARGV[3]), tonumber(ARGV[4])

    local last = redis.call('GET', kts)
    local avg, dt

    if last ~= false then
        avg = redis.call('GET', kavg)
        if avg == false then avg = 0 else avg = tonumber(avg) end
        dt = now - tonumber(last)
    else
        avg = 0
        dt = period
    end
    if dt == 0 then dt = 1 end

    local a = math.exp(-dt/period)
    local rate = cost * period / dt
    avg = (1 - a) * rate + a * avg

    if avg > max then
        return 1
    else
        local expire = period * 2
        redis.call('SETEX', kts, expire, now)
        redis.call('SETEX', kavg, expire, avg)
        return 0
    end
end

local ewma = {}

function ewma.ewma(...)
    local arg = {...}
    local nkeys = 0
    local KEYS = {}
    local ARGV = {}

    for i, v in ipairs(arg) do
        if i == 1 then
            nkeys = tonumber(v)
        elseif i <= nkeys + 1 then
            KEYS[#KEYS+1] = v
        else
            ARGV[#ARGV+1] = v
        end
    end

    return do_ewma(KEYS, ARGV)
end

return ewma
