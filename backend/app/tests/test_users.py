# import pytest
# from fastapi.testclient import TestClient
# from sqlalchemy import create_engine
# from sqlalchemy.orm import sessionmaker
# from ..main import app
# from ..database.connection import get_db, Base
# from ..database.schemas import UserCreate

# # Use an in-memory SQLite database for testing
# SQLALCHEMY_DATABASE_URL = "sqlite:///./test.db"

# engine = create_engine(SQLALCHEMY_DATABASE_URL, connect_args={"check_same_thread": False})
# TestingSessionLocal = sessionmaker(autocommit=False, autoflush=False, bind=engine)

# Base.metadata.create_all(bind=engine)

# def override_get_db():
#     try:
#         db = TestingSessionLocal()
#         yield db
#     finally:
#         db.close()

# app.dependency_overrides[get_db] = override_get_db

# client = TestClient(app)


# def test_register_user():
#     user_data = {
#         "email": "test5@example.com",
#         "first_name": "Test",
#         "last_name": "User",
#         "password": "testpassword"
#     }
#     response = client.post("/users/register", json=user_data)
#     assert response.status_code == 200
#     data = response.json()
#     assert data["email"] == user_data["email"]
#     assert data["first_name"] == user_data["first_name"]
#     assert data["last_name"] == user_data["last_name"]
#     assert "id" in data
#     assert "created_at" in data
#     assert "updated_at" in data

# # def test_register_user_duplicate_email():
# #     user_data = {
# #         "email": "duplicate@example.com",
# #         "first_name": "Duplicate",
# #         "last_name": "User",
# #         "password": "testpassword"
# #     }
# #     # Register the user for the first time
# #     response = client.post("/users/register", json=user_data)
# #     assert response.status_code == 200

# #     # Try to register the same email again
# #     response = client.post("/users/register", json=user_data)
# #     assert response.status_code == 400
# #     assert "detail" in response.json()

# def test_duplicate_email():
#     user_data = {
#         "email": "test@example.com",
#         "first_name": "Test",
#         "last_name": "User",
#         "password": "testpassword"
#     }
#     # Try to register the same email again
#     response = client.post("/users/register", json=user_data)
#     assert response.status_code == 400, f"Expected 400, got {response.status_code}. Response content: {response.content}"
#     assert "Email already registered" in response.json()["detail"]