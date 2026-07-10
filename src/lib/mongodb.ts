import { MongoClient, Db } from "mongodb";

const uri = process.env.MONGODB_URI;

let client: MongoClient | null = null;
let clientPromise: Promise<MongoClient> | null = null;

function getClientPromise(): Promise<MongoClient> {
  if (clientPromise) return clientPromise;

  const mongoUri = uri || "mongodb://localhost:27017/r2-uplink";

  if (process.env.NODE_ENV === "development") {
    const globalWithMongo = global as typeof globalThis & {
      _mongoClientPromise?: Promise<MongoClient>;
    };

    if (!globalWithMongo._mongoClientPromise) {
      client = new MongoClient(mongoUri);
      globalWithMongo._mongoClientPromise = client.connect();
    }
    clientPromise = globalWithMongo._mongoClientPromise;
  } else {
    client = new MongoClient(mongoUri);
    clientPromise = client.connect();
  }

  return clientPromise;
}

export async function getDb(): Promise<Db> {
  if (process.env.NODE_ENV === "production" && !uri) {
    throw new Error("MONGODB_URI environment variable is missing.");
  }
  const connection = await getClientPromise();
  return connection.db();
}

// Helper to initialize indexes
export async function initIndexes() {
  try {
    const db = await getDb();
    
    // Unique shareId index
    await db.collection("shares").createIndex({ shareId: 1 }, { unique: true });
    
    // Cleanup compound index
    await db.collection("shares").createIndex({ status: 1, expiresAt: 1 });
    
    // Integrity index for CAS
    await db.collection("shares").createIndex({ hashValue: 1 });
    
    console.log("MongoDB Indexes initialized successfully.");
  } catch (error) {
    console.error("Failed to initialize MongoDB indexes:", error);
  }
}
