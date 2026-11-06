import http from "k6/http";
import { check, fail } from "k6";

export let options = {
  scenarios: {
    students: {
      executor: "shared-iterations",
      vus: 40,
      iterations: 2000,
      maxDuration: "10m"
    }
  }
};

var BASE_URL = __ENV.BASE_URL || "http://127.0.0.1:8081";
var PASSWORD = "Pass123!";

function login(loginName) {
  var res = http.post(
    BASE_URL + "/login",
    JSON.stringify({
      login: loginName,
      password: PASSWORD
    }),
    {
      headers: {
        "Content-Type": "application/json"
      }
    }
  );

  check(res, {
    "login status 200": function (r) {
      return r.status === 200;
    }
  });

  if (res.status !== 200) {
    fail("login failed for " + loginName + ": " + res.status + " " + res.body);
  }

  return res.json();
}

export default function () {
  var idx = String(__ITER + 1).padStart(5, "0");
  var loginName = "student_" + idx;

  var auth = login(loginName);

  var coursesRes = http.get(
    BASE_URL + "/student/courses?user_uuid=" + encodeURIComponent(auth.uuid) +
    "&user_role=student"
  );

  check(coursesRes, {
    "courses status 200": function (r) {
      return r.status === 200;
    }
  });

  if (coursesRes.status !== 200) {
    fail("courses failed for " + loginName + ": " + coursesRes.status + " " + coursesRes.body);
  }

  var courses = coursesRes.json();
  if (!courses || !courses.length) {
    fail("no courses for " + loginName);
  }

  var courseUuid = courses[0].course_uuid;

  var courseRes = http.get(
    BASE_URL + "/student/course?user_uuid=" + encodeURIComponent(auth.uuid) +
    "&user_role=student&course_uuid=" + encodeURIComponent(courseUuid)
  );

  check(courseRes, {
    "course status 200": function (r) {
      return r.status === 200;
    }
  });

  if (courseRes.status !== 200) {
    fail("course open failed for " + loginName + ": " + courseRes.status + " " + courseRes.body);
  }

  var courseData = courseRes.json();
  if (!courseData.tasks || !courseData.tasks.length) {
    fail("no tasks in course for " + loginName);
  }

  var taskUuid = courseData.tasks[0].task_uuid;

  var submitPayload = {
    course_uuid: courseUuid,
    task_uuid: taskUuid,
    file: http.file(
      'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Print("OK")\n}\n',
      "main.go",
      "text/plain"
    ),
    language: "go",
    version: "go1.25",
    user_uuid: auth.uuid,
    user_role: "student"
  };

  var submitRes = http.post(BASE_URL + "/check-file", submitPayload);

  check(submitRes, {
    "submit status 200": function (r) {
      return r.status === 200;
    }
  });

  if (submitRes.status !== 200) {
    fail("submit failed for " + loginName + ": " + submitRes.status + " " + submitRes.body);
  }
}