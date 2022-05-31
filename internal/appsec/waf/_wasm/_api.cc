#include <unistd.h>
#include <ddwaf.h>
#include <yaml-cpp/yaml.h>

#define DDWAF_OBJECT_INVALID                    \
    {                                           \
        NULL, 0, { NULL }, 0, DDWAF_OBJ_INVALID \
    }
#define DDWAF_OBJECT_MAP                    \
    {                                       \
        NULL, 0, { NULL }, 0, DDWAF_OBJ_MAP \
    }
#define DDWAF_OBJECT_ARRAY                    \
    {                                         \
        NULL, 0, { NULL }, 0, DDWAF_OBJ_ARRAY \
    }
#define DDWAF_OBJECT_SIGNED_FORCE(value)                      \
    {                                                         \
        NULL, 0, { (const char*) value }, 0, DDWAF_OBJ_SIGNED \
    }
#define DDWAF_OBJECT_UNSIGNED_FORCE(value)                      \
    {                                                           \
        NULL, 0, { (const char*) value }, 0, DDWAF_OBJ_UNSIGNED \
    }
#define DDWAF_OBJECT_STRING_PTR(string, length)       \
    {                                                 \
        NULL, 0, { string }, length, DDWAF_OBJ_STRING \
    }

namespace YAML
{

class parsing_error : public std::exception
{
public:
    parsing_error(const std::string& what) : what_(what) {}
    const char* what() const noexcept { return what_.c_str(); }

protected:
    const std::string what_;
};

ddwaf_object node_to_arg(const Node& node)
{
    switch (node.Type())
    {
        case NodeType::Sequence:
        {
            ddwaf_object arg = DDWAF_OBJECT_ARRAY;
            for (auto it = node.begin(); it != node.end(); ++it)
            {
                ddwaf_object child = node_to_arg(*it);
                ddwaf_object_array_add(&arg, &child);
            }
            return arg;
        }
        case NodeType::Map:
        {
            ddwaf_object arg = DDWAF_OBJECT_MAP;
            for (auto it = node.begin(); it != node.end(); ++it)
            {
                std::string key    = it->first.as<std::string>();
                ddwaf_object child = node_to_arg(it->second);
                ddwaf_object_map_addl(&arg, key.c_str(), key.size(), &child);
            }
            return arg;
        }
        case NodeType::Scalar:
        {
            const std::string& value = node.Scalar();
            ddwaf_object arg;
            ddwaf_object_stringl(&arg, value.c_str(), value.size());
            return arg;
        }
        case NodeType::Null:
        case NodeType::Undefined:
            ddwaf_object arg = DDWAF_OBJECT_MAP;
            return arg;
    }

    throw parsing_error("Invalid YAML node type");
}

// template helpers
template <>
as_if<ddwaf_object, void>::as_if(const Node& node_) : node(node_) {}

template <>
ddwaf_object as_if<ddwaf_object, void>::operator()() const
{
    return node_to_arg(node);
}
}

extern "C"
ddwaf_object* my_ddwaf_encode(const char* rule)
{
    YAML::Node doc = YAML::Load(rule);
    ddwaf_object o = doc.as<ddwaf_object>();
    if (o.type == DDWAF_OBJ_INVALID)
    {
        return NULL;
    }
    auto res = new(ddwaf_object);
    *res = o;
    return res;
}

extern "C"
const char* my_ddwaf_run(ddwaf_context ctx, ddwaf_object* data)
{
    ddwaf_result res;
    ddwaf_run(ctx, data, &res, 1000000000);
    return res.data;
}

void logger(DDWAF_LOG_LEVEL level, const char* function, const char* file, unsigned line,
    const char* message, uint64_t message_len)
{
    write(1, message, message_len);
    write(1, "\n", 1);
}

extern "C"
void my_ddwaf_set_logger()
{
    ddwaf_set_log_cb(logger, DDWAF_LOG_TRACE);
}
