#include <yaml-cpp/yaml.h>
#include <ddwaf.h>
#include <emscripten/bind.h>

using namespace emscripten;

ddwaf_object parseYAML(std::string buf)
{
  YAML::Node doc = YAML::Load(buf);
  return doc.as<ddwaf_object>();
}

uintptr_t mwaf_init(std::string rules) {
  auto r = parseYAML(rules);
  if (r.type == DDWAF_OBJ_INVALID) {
    return NULL;
  }
  auto h = ddwaf_init(&r, NULL, NULL);
  ddwaf_object_free(&r);
  return reinterpret_cast<uintptr_t>(h);
}

std::string mwaf_run(uintptr_t handle, std::string data) {
  if (handle == NULL) {
    return "null";
  }
  auto h = reinterpret_cast<ddwaf_handle>(handle);
  auto ctx = ddwaf_context_init(h, NULL);
  if (ctx == NULL) {
    return "null";
  }
  auto d = parseYAML(data);
  ddwaf_result res;
  ddwaf_run(ctx, &d, &res, 1000000);
  ddwaf_object_free(&d);
  ddwaf_context_destroy(ctx);
  std::string events;
  if (res.data == NULL) {
    events = "null";
  } else {
    events = std::string(res.data);
  }
  ddwaf_result_free(&res);
  return events;
}

void mwaf_destroy(uintptr_t handle) {
  if (handle == NULL) {
    return;
  }
  auto h = reinterpret_cast<ddwaf_handle>(handle);
  ddwaf_destroy(h);
}


EMSCRIPTEN_BINDINGS(mwaf) {
  function("init", &mwaf_init);
  function("run", &mwaf_run);
  function("destroy", &mwaf_destroy);
}

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
